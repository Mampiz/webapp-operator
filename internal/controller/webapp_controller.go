/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	platformv1 "github.com/Mampiz/webapp-operator/api/v1"
)

// WebAppReconciler reconciles a WebApp object
type WebAppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=platform.miportfolio.com,resources=webapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.miportfolio.com,resources=webapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.miportfolio.com,resources=webapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete

// Reconcile ensures a Deployment, a Service and (optionally) an HPA exist to
// match the WebApp spec.
func (r *WebAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var webapp platformv1.WebApp
	if err := r.Get(ctx, req.NamespacedName, &webapp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Labels shared by the Deployment selector, the pod template and the Service.
	labels := map[string]string{"app": webapp.Name}

	// --- Deployment ---
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webapp.Name + "-deployment",
			Namespace: webapp.Namespace,
		},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		// Only manage replicas ourselves when autoscaling is OFF; otherwise the
		// HPA owns the replica count and we must not fight it on every reconcile.
		if webapp.Spec.Autoscaling == nil {
			deploy.Spec.Replicas = ptr.To(webapp.Spec.Replicas)
		}
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		deploy.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "webapp",
					Image: webapp.Spec.Image,
					Ports: []corev1.ContainerPort{{
						Name:          "http",
						Protocol:      corev1.ProtocolTCP,
						ContainerPort: webapp.Spec.Port,
					}},
				}},
			},
		}
		return controllerutil.SetControllerReference(&webapp, deploy, r.Scheme)
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	log.Info("reconciled deployment", "operation", op, "name", deploy.Name)

	// --- Service ---
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webapp.Name + "-service",
			Namespace: webapp.Namespace,
		},
	}
	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
		service.Spec.Selector = labels
		service.Spec.Type = corev1.ServiceTypeClusterIP
		service.Spec.Ports = []corev1.ServicePort{{
			Name:       "http",
			Protocol:   corev1.ProtocolTCP,
			Port:       webapp.Spec.Port,
			TargetPort: intstr.FromInt32(webapp.Spec.Port),
		}}
		return controllerutil.SetControllerReference(&webapp, service, r.Scheme)
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	log.Info("reconciled service", "operation", op, "name", service.Name)

	// --- HorizontalPodAutoscaler ---
	if webapp.Spec.Autoscaling != nil {
		hpa := &autoscalingv2.HorizontalPodAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Name:      webapp.Name + "-autoscaler",
				Namespace: webapp.Namespace,
			},
		}
		op, err = controllerutil.CreateOrUpdate(ctx, r.Client, hpa, func() error {
			hpa.Spec.ScaleTargetRef = autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       deploy.Name,
			}
			hpa.Spec.MinReplicas = ptr.To(webapp.Spec.Autoscaling.MinReplicas)
			hpa.Spec.MaxReplicas = webapp.Spec.Autoscaling.MaxReplicas
			hpa.Spec.Metrics = []autoscalingv2.MetricSpec{{
				Type: autoscalingv2.ResourceMetricSourceType,
				Resource: &autoscalingv2.ResourceMetricSource{
					Name: corev1.ResourceCPU,
					Target: autoscalingv2.MetricTarget{
						Type:               autoscalingv2.UtilizationMetricType,
						AverageUtilization: ptr.To(webapp.Spec.Autoscaling.CPUThresholdPercent),
					},
				},
			}}
			return controllerutil.SetControllerReference(&webapp, hpa, r.Scheme)
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		log.Info("reconciled hpa", "operation", op, "name", hpa.Name)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1.WebApp{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Named("webapp").
		Complete(r)
}
