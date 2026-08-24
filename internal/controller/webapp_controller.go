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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
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
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme
}

// +kubebuilder:rbac:groups=platform.miportfolio.com,resources=webapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.miportfolio.com,resources=webapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.miportfolio.com,resources=webapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete

// Reconcile ensures a Deployment, a Service and (optionally) an HPA and a
// PodDisruptionBudget exist to match the WebApp spec, and reports the observed
// state back in the WebApp status.
func (r *WebAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	log := logf.FromContext(ctx)

	// Record exactly one reconcile outcome per call, whatever return path we take.
	// Named return values let the deferred func read the final error.
	defer func() {
		if err != nil {
			reconcileTotal.WithLabelValues("error").Inc()
		} else {
			reconcileTotal.WithLabelValues("success").Inc()
		}
	}()

	var webapp platformv1.WebApp
	if getErr := r.Get(ctx, req.NamespacedName, &webapp); getErr != nil {
		if apierrors.IsNotFound(getErr) {
			// The WebApp is gone: drop its operand series so deleted objects do
			// not linger forever in Prometheus.
			forgetWebAppMetrics(req.Namespace, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, getErr
	}

	// Labels shared by the Deployment selector, the pod template, the Service
	// and the PodDisruptionBudget selector.
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
			deploy.Spec.Replicas = ptr.To(desiredReplicas(&webapp))
		}
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		deploy.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				SecurityContext: podSecurityContext(&webapp),
				Containers: []corev1.Container{{
					Name:  "webapp",
					Image: webapp.Spec.Image,
					Ports: []corev1.ContainerPort{{
						Name:          "http",
						Protocol:      corev1.ProtocolTCP,
						ContainerPort: webapp.Spec.Port,
					}},
					Resources:       containerResources(&webapp),
					ReadinessProbe:  readinessProbe(&webapp),
					SecurityContext: containerSecurityContext(&webapp),
				}},
			},
		}
		return controllerutil.SetControllerReference(&webapp, deploy, r.Scheme)
	})
	if err != nil {
		r.fail(ctx, &webapp, "Deployment", err)
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		r.Recorder.Eventf(&webapp, corev1.EventTypeNormal, "DeploymentReconciled", "Deployment %s %s", deploy.Name, op)
		childOperationsTotal.WithLabelValues("deployment", string(op)).Inc()
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
		r.fail(ctx, &webapp, "Service", err)
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		r.Recorder.Eventf(&webapp, corev1.EventTypeNormal, "ServiceReconciled", "Service %s %s", service.Name, op)
		childOperationsTotal.WithLabelValues("service", string(op)).Inc()
	}
	log.Info("reconciled service", "operation", op, "name", service.Name)

	// --- HorizontalPodAutoscaler (only if autoscaling is configured) ---
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webapp.Name + "-autoscaler",
			Namespace: webapp.Namespace,
		},
	}
	if webapp.Spec.Autoscaling != nil {
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
			r.fail(ctx, &webapp, "HorizontalPodAutoscaler", err)
			return ctrl.Result{}, err
		}
		if op != controllerutil.OperationResultNone {
			r.Recorder.Eventf(&webapp, corev1.EventTypeNormal, "HPAReconciled", "HorizontalPodAutoscaler %s %s", hpa.Name, op)
			childOperationsTotal.WithLabelValues("hpa", string(op)).Inc()
		}
		log.Info("reconciled hpa", "operation", op, "name", hpa.Name)
	} else if err = r.deleteIfExists(ctx, hpa); err != nil {
		// Removing the autoscaling block from the spec must remove the HPA:
		// owner references only clean up when the *parent* is deleted.
		r.fail(ctx, &webapp, "HorizontalPodAutoscaler", err)
		return ctrl.Result{}, err
	}

	// --- PodDisruptionBudget (only if configured) ---
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webapp.Name + "-pdb",
			Namespace: webapp.Namespace,
		},
	}
	if webapp.Spec.PodDisruptionBudget != nil {
		op, err = controllerutil.CreateOrUpdate(ctx, r.Client, pdb, func() error {
			pdb.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
			pdb.Spec.MinAvailable = webapp.Spec.PodDisruptionBudget.MinAvailable
			pdb.Spec.MaxUnavailable = webapp.Spec.PodDisruptionBudget.MaxUnavailable
			return controllerutil.SetControllerReference(&webapp, pdb, r.Scheme)
		})
		if err != nil {
			r.fail(ctx, &webapp, "PodDisruptionBudget", err)
			return ctrl.Result{}, err
		}
		if op != controllerutil.OperationResultNone {
			r.Recorder.Eventf(&webapp, corev1.EventTypeNormal, "PDBReconciled", "PodDisruptionBudget %s %s", pdb.Name, op)
			childOperationsTotal.WithLabelValues("pdb", string(op)).Inc()
		}
	} else if err = r.deleteIfExists(ctx, pdb); err != nil {
		r.fail(ctx, &webapp, "PodDisruptionBudget", err)
		return ctrl.Result{}, err
	}

	// --- Status ---
	if err = r.updateStatus(ctx, &webapp, deploy); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// desiredReplicas returns the replica count to use, tolerating a nil spec value.
// The defaulting webhook normally fills this in, but the controller must not
// depend on the webhook being deployed.
func desiredReplicas(webapp *platformv1.WebApp) int32 {
	if webapp.Spec.Replicas == nil {
		return 1
	}
	return *webapp.Spec.Replicas
}

// defaultCPURequest is applied to the container when autoscaling is enabled and
// no CPU request was given. It matches the value the defaulting webhook writes
// onto the stored object.
const defaultCPURequest = "100m"

// containerResources returns the compute resources for the application container.
//
// CPU-based autoscaling is computed as a percentage of the CPU *request*, so an
// HPA without one can never scale. The defaulting webhook normally supplies it,
// but webhooks are optional (the Helm chart ships them disabled, since they need
// cert-manager). A controller must never assume admission ran, so the request is
// applied here as well: correctness cannot depend on an optional component.
func containerResources(webapp *platformv1.WebApp) corev1.ResourceRequirements {
	resources := corev1.ResourceRequirements{}
	if webapp.Spec.Resources != nil {
		resources = *webapp.Spec.Resources.DeepCopy()
	}
	if webapp.Spec.Autoscaling == nil {
		return resources
	}
	if _, ok := resources.Requests[corev1.ResourceCPU]; ok {
		return resources
	}
	if resources.Requests == nil {
		resources.Requests = corev1.ResourceList{}
	}
	resources.Requests[corev1.ResourceCPU] = resource.MustParse(defaultCPURequest)
	return resources
}

// readinessProbe builds the readiness probe for the application container.
// An HTTP GET is used when the user declares a path, otherwise a TCP connection
// to the port, which is the strongest check that is safe for an arbitrary image.
func readinessProbe(webapp *platformv1.WebApp) *corev1.Probe {
	port := intstr.FromInt32(webapp.Spec.Port)
	handler := corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: port}}
	if webapp.Spec.ReadinessPath != "" {
		handler = corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
			Path: webapp.Spec.ReadinessPath,
			Port: port,
		}}
	}
	return &corev1.Probe{
		ProbeHandler:        handler,
		InitialDelaySeconds: 3,
		PeriodSeconds:       10,
		TimeoutSeconds:      2,
		FailureThreshold:    3,
	}
}

// podSecurityContext applies the pod-level hardening. Only the image-dependent
// knobs are configurable; see SecuritySpec for the rationale.
func podSecurityContext(webapp *platformv1.WebApp) *corev1.PodSecurityContext {
	sc := &corev1.PodSecurityContext{
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
	if sec := webapp.Spec.Security; sec != nil {
		sc.RunAsNonRoot = sec.RunAsNonRoot
		sc.RunAsUser = sec.RunAsUser
	}
	return sc
}

// containerSecurityContext applies the container-level hardening that is safe
// for any image, plus the opt-in read-only root filesystem.
func containerSecurityContext(webapp *platformv1.WebApp) *corev1.SecurityContext {
	sc := &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
	if sec := webapp.Spec.Security; sec != nil {
		sc.RunAsNonRoot = sec.RunAsNonRoot
		sc.RunAsUser = sec.RunAsUser
		sc.ReadOnlyRootFilesystem = sec.ReadOnlyRootFilesystem
	}
	return sc
}

// deleteIfExists removes an object the spec no longer asks for. A missing object
// is the desired end state, so NotFound is not an error.
func (r *WebAppReconciler) deleteIfExists(ctx context.Context, obj client.Object) error {
	if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// fail records a Degraded condition and a Warning event for a failed child
// reconciliation. Status reporting is best-effort: the original error is what
// gets returned to the work queue.
func (r *WebAppReconciler) fail(ctx context.Context, webapp *platformv1.WebApp, kind string, cause error) {
	r.Recorder.Eventf(webapp, corev1.EventTypeWarning, "ReconcileFailed", "failed to reconcile %s: %v", kind, cause)

	original := webapp.DeepCopy()
	meta.SetStatusCondition(&webapp.Status.Conditions, metav1.Condition{
		Type:               platformv1.ConditionDegraded,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: webapp.Generation,
		Reason:             kind + "ReconcileFailed",
		Message:            cause.Error(),
	})
	if err := r.Status().Patch(ctx, webapp, client.MergeFrom(original)); err != nil {
		logf.FromContext(ctx).Error(err, "failed to record Degraded condition")
	}
}

// updateStatus reports the observed state of the operand back on the WebApp.
// It patches instead of updating so a concurrent writer touching other fields
// does not cause a lost update, and it only issues a request when something
// actually changed.
func (r *WebAppReconciler) updateStatus(ctx context.Context, webapp *platformv1.WebApp, deploy *appsv1.Deployment) error {
	original := webapp.DeepCopy()

	ready := deploy.Status.ReadyReplicas
	total := deploy.Status.Replicas
	available := ready > 0 && ready == total
	rollingOut := total == 0 || deploy.Status.UpdatedReplicas != total || ready != total

	webapp.Status.ReadyReplicas = ready
	webapp.Status.ObservedGeneration = webapp.Generation

	availableCondition := metav1.Condition{
		Type:               platformv1.ConditionAvailable,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: webapp.Generation,
		Reason:             "DeploymentNotReady",
		Message:            fmt.Sprintf("%d/%d replicas ready", ready, total),
	}
	if available {
		availableCondition.Status = metav1.ConditionTrue
		availableCondition.Reason = "DeploymentReady"
		availableCondition.Message = "all replicas are ready"
	}
	meta.SetStatusCondition(&webapp.Status.Conditions, availableCondition)

	progressingCondition := metav1.Condition{
		Type:               platformv1.ConditionProgressing,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: webapp.Generation,
		Reason:             "RolloutComplete",
		Message:            "the Deployment has finished rolling out",
	}
	if rollingOut {
		progressingCondition.Status = metav1.ConditionTrue
		progressingCondition.Reason = "RolloutInProgress"
		progressingCondition.Message = fmt.Sprintf("%d/%d replicas updated and ready", deploy.Status.UpdatedReplicas, total)
	}
	meta.SetStatusCondition(&webapp.Status.Conditions, progressingCondition)

	// Reaching this point means every child reconciled, so we are not degraded.
	meta.SetStatusCondition(&webapp.Status.Conditions, metav1.Condition{
		Type:               platformv1.ConditionDegraded,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: webapp.Generation,
		Reason:             "ReconcileSucceeded",
		Message:            "all managed resources reconciled",
	})

	recordWebAppMetrics(webapp, ready)

	if equalStatus(&original.Status, &webapp.Status) {
		return nil
	}
	return r.Status().Patch(ctx, webapp, client.MergeFrom(original))
}

// equalStatus reports whether two statuses are semantically identical.
// meta.SetStatusCondition only moves LastTransitionTime when a condition really
// changes, so a plain deep comparison is stable across no-op reconciles.
func equalStatus(a, b *platformv1.WebAppStatus) bool {
	return equality.Semantic.DeepEqual(a, b)
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1.WebApp{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Named("webapp").
		Complete(r)
}
