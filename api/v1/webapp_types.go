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

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Condition types reported in WebAppStatus.
const (
	// ConditionAvailable is True when the desired replicas are ready and serving.
	ConditionAvailable = "Available"
	// ConditionProgressing is True while the Deployment is still rolling out.
	ConditionProgressing = "Progressing"
	// ConditionDegraded is True when reconciliation failed to reach the desired state.
	ConditionDegraded = "Degraded"
)

// AutoscalingSpec defines the autoscaling behaviour for the WebApp.
//
// When set, the HorizontalPodAutoscaler owns the replica count and spec.replicas
// is ignored. A CPU request is required for CPU-based autoscaling to work; one is
// defaulted by the admission webhook on the stored object, and independently by
// the reconciler on the Deployment, so autoscaling works even where the optional
// webhooks are not installed.
// +kubebuilder:validation:XValidation:rule="self.maxReplicas >= self.minReplicas",message="maxReplicas must be greater than or equal to minReplicas"
type AutoscalingSpec struct {
	// MinReplicas is the minimum number of replicas the autoscaler will scale down to.
	// +kubebuilder:validation:Minimum=1
	MinReplicas int32 `json:"minReplicas"`
	// MaxReplicas is the maximum number of replicas the autoscaler will scale up to.
	// +kubebuilder:validation:Minimum=1
	MaxReplicas int32 `json:"maxReplicas"`
	// CPUThresholdPercent is the target average CPU utilization, expressed as a
	// percentage of the container's CPU *request*.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	CPUThresholdPercent int32 `json:"cpuThresholdPercent"`
}

// SecuritySpec exposes the pod hardening knobs that depend on the image itself.
//
// Hardening that is safe for any image (dropping all capabilities, disallowing
// privilege escalation, the RuntimeDefault seccomp profile) is always applied and
// is deliberately not configurable. The settings below are opt-in because they
// break images that legitimately need to write to the root filesystem or start
// as root, so enabling them blindly would make the operator unusable.
type SecuritySpec struct {
	// RunAsNonRoot rejects the container if the image would start as root.
	// Requires an image that declares a non-root USER.
	// +optional
	RunAsNonRoot *bool `json:"runAsNonRoot,omitempty"`
	// RunAsUser overrides the UID the container runs as.
	// +kubebuilder:validation:Minimum=1
	// +optional
	RunAsUser *int64 `json:"runAsUser,omitempty"`
	// ReadOnlyRootFilesystem mounts the container root filesystem read-only.
	// Images that write to disk at runtime need writable volumes instead.
	// +optional
	ReadOnlyRootFilesystem *bool `json:"readOnlyRootFilesystem,omitempty"`
}

// PodDisruptionBudgetSpec configures a PodDisruptionBudget for the WebApp pods,
// protecting availability during voluntary disruptions such as node drains.
// +kubebuilder:validation:XValidation:rule="has(self.minAvailable) != has(self.maxUnavailable)",message="exactly one of minAvailable or maxUnavailable must be set"
type PodDisruptionBudgetSpec struct {
	// MinAvailable is the number or percentage of pods that must stay available.
	// +optional
	MinAvailable *intstr.IntOrString `json:"minAvailable,omitempty"`
	// MaxUnavailable is the number or percentage of pods that may be unavailable.
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
}

// WebAppSpec defines the desired state of WebApp.
type WebAppSpec struct {
	// Image is the container image to run. The tag must be explicit and immutable:
	// "latest" and untagged references are rejected by the validating webhook so
	// that deployments stay reproducible. Digests (image@sha256:...) are allowed.
	// +kubebuilder:validation:MinLength=1
	// The optional leading group is the registry host, which may carry a port
	// (myregistry:5000/app:1.0) and must not be mistaken for a tag.
	// +kubebuilder:validation:Pattern=`^([a-zA-Z0-9._-]+(:[0-9]+)?/)?[a-zA-Z0-9][a-zA-Z0-9._/-]*(:[a-zA-Z0-9._-]+)?(@sha256:[a-f0-9]{64})?$`
	Image string `json:"image"`

	// Replicas is the desired number of replicas. Ignored when autoscaling is set,
	// because the HorizontalPodAutoscaler owns the replica count in that case.
	// Defaults to 1 when omitted. The default is declared in the schema, so the
	// API server applies it whether or not the admission webhooks are installed.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Port is the port the application listens on.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Autoscaling enables a HorizontalPodAutoscaler for this WebApp.
	// +optional
	Autoscaling *AutoscalingSpec `json:"autoscaling,omitempty"`

	// Resources are the compute resources for the application container.
	//
	// A CPU request is required for CPU-based autoscaling, because HPA target
	// utilization is a percentage of the request. It cannot be expressed as a
	// schema default because it is conditional on spec.autoscaling being set, so
	// the defaulting webhook fills it in on the stored object and the reconciler
	// applies the same value to the Deployment when the webhooks are not
	// installed. See docs/design.md.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// ReadinessPath is the HTTP path probed for readiness on spec.port. When
	// omitted a TCP connection to spec.port is used instead, which works for any
	// listening process without assuming an HTTP endpoint exists.
	// +kubebuilder:validation:Pattern=`^/.*$`
	// +optional
	ReadinessPath string `json:"readinessPath,omitempty"`

	// Security configures the image-dependent pod hardening options.
	// +optional
	Security *SecuritySpec `json:"security,omitempty"`

	// PodDisruptionBudget creates a PodDisruptionBudget for the WebApp pods.
	// +optional
	PodDisruptionBudget *PodDisruptionBudgetSpec `json:"podDisruptionBudget,omitempty"`
}

// WebAppStatus defines the observed state of WebApp.
type WebAppStatus struct {
	// conditions represent the current state of the WebApp resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Condition types reported by this operator:
	// - "Available": the desired replicas are ready
	// - "Progressing": the Deployment is still rolling out
	// - "Degraded": reconciliation failed to reach the desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// readyReplicas is the number of pods currently ready behind this WebApp.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// observedGeneration is the .metadata.generation this status was computed from.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Available",type=string,JSONPath=`.status.conditions[?(@.type=="Available")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WebApp is the Schema for the webapps API
type WebApp struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of WebApp
	// +required
	Spec WebAppSpec `json:"spec"`

	// status defines the observed state of WebApp
	// +optional
	Status WebAppStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// WebAppList contains a list of WebApp
type WebAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []WebApp `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &WebApp{}, &WebAppList{})
		return nil
	})
}
