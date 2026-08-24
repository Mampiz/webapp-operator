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
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	platformv1 "github.com/Mampiz/webapp-operator/api/v1"
)

// log is for logging in this package.
var webapplog = logf.Log.WithName("webapp-resource")

// SetupWebAppWebhookWithManager registers the webhook for WebApp in the manager.
func SetupWebAppWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &platformv1.WebApp{}).
		WithValidator(&WebAppCustomValidator{}).
		WithDefaulter(&WebAppCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-platform-miportfolio-com-v1-webapp,mutating=true,failurePolicy=fail,sideEffects=None,groups=platform.miportfolio.com,resources=webapps,verbs=create;update,versions=v1,name=mwebapp-v1.kb.io,admissionReviewVersions=v1

// WebAppCustomDefaulter sets default values on WebApp resources on create/update.
type WebAppCustomDefaulter struct{}

const (
	// managedByLabel marks resources that this operator is responsible for.
	managedByLabel = "app.kubernetes.io/managed-by"
	// defaultReplicas is used when spec.replicas is omitted.
	defaultReplicas int32 = 1
	// defaultCPURequest is injected when autoscaling is enabled but no CPU request
	// was given. HPA target utilization is a percentage of the request, so without
	// one the HPA can never compute a value and never scales. 100m is small enough
	// not to waste quota and large enough to produce a meaningful ratio.
	defaultCPURequest = "100m"
)

// Default implements webhook.CustomDefaulter. It runs before schema validation,
// so it can fill in values that are then validated as if the user had set them.
func (d *WebAppCustomDefaulter) Default(_ context.Context, obj *platformv1.WebApp) error {
	webapplog.Info("Defaulting for WebApp", "name", obj.GetName())

	// Stamp a managed-by label if the user did not set one, so every WebApp is
	// easy to find. This is a mutating operation: it changes the stored object.
	if obj.Labels == nil {
		obj.Labels = map[string]string{}
	}
	if _, ok := obj.Labels[managedByLabel]; !ok {
		obj.Labels[managedByLabel] = "webapp-operator"
	}

	// spec.replicas is optional; a WebApp without an explicit count means "one".
	if obj.Spec.Replicas == nil {
		obj.Spec.Replicas = ptr.To(defaultReplicas)
	}

	// CPU-based autoscaling is meaningless without a CPU request: make the
	// resource self-consistent instead of silently shipping an HPA that reports
	// <unknown> forever.
	if obj.Spec.Autoscaling != nil {
		if obj.Spec.Resources == nil {
			obj.Spec.Resources = &corev1.ResourceRequirements{}
		}
		if obj.Spec.Resources.Requests == nil {
			obj.Spec.Resources.Requests = corev1.ResourceList{}
		}
		if _, ok := obj.Spec.Resources.Requests[corev1.ResourceCPU]; !ok {
			obj.Spec.Resources.Requests[corev1.ResourceCPU] = resource.MustParse(defaultCPURequest)
		}
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-platform-miportfolio-com-v1-webapp,mutating=false,failurePolicy=fail,sideEffects=None,groups=platform.miportfolio.com,resources=webapps,verbs=create;update,versions=v1,name=vwebapp-v1.kb.io,admissionReviewVersions=v1

// WebAppCustomValidator validates WebApp resources on create/update/delete.
type WebAppCustomValidator struct{}

// ValidateCreate validates a WebApp when it is created.
func (v *WebAppCustomValidator) ValidateCreate(_ context.Context, obj *platformv1.WebApp) (admission.Warnings, error) {
	webapplog.Info("Validation for WebApp upon creation", "name", obj.GetName())
	return nil, validateImageTag(obj)
}

// ValidateUpdate validates a WebApp when it is updated.
func (v *WebAppCustomValidator) ValidateUpdate(_ context.Context, _, newObj *platformv1.WebApp) (admission.Warnings, error) {
	webapplog.Info("Validation for WebApp upon update", "name", newObj.GetName())
	return nil, validateImageTag(newObj)
}

// ValidateDelete validates a WebApp when it is deleted. Nothing to check.
func (v *WebAppCustomValidator) ValidateDelete(_ context.Context, obj *platformv1.WebApp) (admission.Warnings, error) {
	webapplog.Info("Validation for WebApp upon deletion", "name", obj.GetName())
	return nil, nil
}

// validateImageTag rejects images that use a mutable reference. A digest or an
// explicit, non-"latest" tag is required so deployments are reproducible.
func validateImageTag(webapp *platformv1.WebApp) error {
	image := webapp.Spec.Image

	// Digest-pinned images (…@sha256:…) are immutable and always allowed.
	if strings.Contains(image, "@sha256:") {
		return nil
	}

	// Only look at the part after the last "/" so a registry port such as
	// "myregistry:5000/app" is not mistaken for a tag.
	name := image[strings.LastIndex(image, "/")+1:]
	colon := strings.LastIndex(name, ":")
	if colon == -1 {
		return fmt.Errorf("spec.image %q must specify an explicit tag (an implicit \"latest\" is not allowed)", image)
	}
	if tag := name[colon+1:]; tag == "latest" {
		return fmt.Errorf("spec.image %q uses the mutable \"latest\" tag; pin an explicit version for reproducibility", image)
	}

	return nil
}
