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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

// Reconcile ensures a Deployment exists that matches the WebApp spec.
func (r *WebAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var webapp platformv1.WebApp
	if err := r.Get(ctx, req.NamespacedName, &webapp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	labels := map[string]string{"app": webapp.Name}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webapp.Name + "-deployment",
			Namespace: webapp.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		// Desired state of the Deployment, derived from the WebApp spec.
		deploy.Spec.Replicas = ptr.To(webapp.Spec.Replicas)
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
		// Make the WebApp the owner of the Deployment: cascade delete + watch.
		return controllerutil.SetControllerReference(&webapp, deploy, r.Scheme)
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	log.Info("reconciled deployment", "operation", op, "name", deploy.Name)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1.WebApp{}).
		Owns(&appsv1.Deployment{}).
		Named("webapp").
		Complete(r)
}
