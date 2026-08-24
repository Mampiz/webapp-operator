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

package v1alpha1

import (
	"fmt"

	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	v1 "github.com/Mampiz/webapp-operator/api/v1"
)

// v1alpha1 is a spoke: it converts to and from the v1 hub. The only shape change
// between the two versions is autoscaling — three flat, individually optional
// fields here, one optional object with three required fields there.

// ConvertTo converts this WebApp (v1alpha1) to the hub version (v1).
func (src *WebApp) ConvertTo(dstRaw conversion.Hub) error {
	dst, ok := dstRaw.(*v1.WebApp)
	if !ok {
		return fmt.Errorf("expected *v1.WebApp, got %T", dstRaw)
	}

	dst.ObjectMeta = src.ObjectMeta
	dst.Spec.Image = src.Spec.Image
	dst.Spec.Replicas = src.Spec.Replicas
	dst.Spec.Port = src.Spec.Port

	// Any of the three flat fields being set meant "autoscaling on". v1 requires
	// all three, so the missing ones are filled with the values the operator
	// would have had to assume anyway. Converting must not fail on data the old
	// schema considered valid: a stored object has to remain readable.
	if src.Spec.MinReplicas != nil || src.Spec.MaxReplicas != nil || src.Spec.CPUThreshold != nil {
		autoscaling := &v1.AutoscalingSpec{
			MinReplicas:         ptr.Deref(src.Spec.MinReplicas, 1),
			MaxReplicas:         ptr.Deref(src.Spec.MaxReplicas, 1),
			CPUThresholdPercent: ptr.Deref(src.Spec.CPUThreshold, 80),
		}
		// v1 enforces maxReplicas >= minReplicas; v1alpha1 never did, so a stored
		// object may violate it. Widen the bound rather than produce something the
		// API server would reject on write.
		if autoscaling.MaxReplicas < autoscaling.MinReplicas {
			autoscaling.MaxReplicas = autoscaling.MinReplicas
		}
		dst.Spec.Autoscaling = autoscaling
	}

	dst.Status.Conditions = src.Status.Conditions
	dst.Status.ReadyReplicas = src.Status.ReadyReplicas
	dst.Status.ObservedGeneration = src.Status.ObservedGeneration

	return nil
}

// ConvertFrom converts the hub version (v1) into this WebApp (v1alpha1).
func (dst *WebApp) ConvertFrom(srcRaw conversion.Hub) error {
	src, ok := srcRaw.(*v1.WebApp)
	if !ok {
		return fmt.Errorf("expected *v1.WebApp, got %T", srcRaw)
	}

	dst.ObjectMeta = src.ObjectMeta
	dst.Spec.Image = src.Spec.Image
	dst.Spec.Replicas = src.Spec.Replicas
	dst.Spec.Port = src.Spec.Port

	if src.Spec.Autoscaling != nil {
		dst.Spec.MinReplicas = ptr.To(src.Spec.Autoscaling.MinReplicas)
		dst.Spec.MaxReplicas = ptr.To(src.Spec.Autoscaling.MaxReplicas)
		dst.Spec.CPUThreshold = ptr.To(src.Spec.Autoscaling.CPUThresholdPercent)
	}

	// Fields v1 gained after v1alpha1 was frozen — resources, readinessPath,
	// security, podDisruptionBudget — have no representation here and are
	// deliberately dropped from the *returned view*. The stored object is v1 and
	// keeps them; reading through v1alpha1 simply cannot show them. This is the
	// cost of serving an old version, and the reason clients should migrate.

	dst.Status.Conditions = src.Status.Conditions
	dst.Status.ReadyReplicas = src.Status.ReadyReplicas
	dst.Status.ObservedGeneration = src.Status.ObservedGeneration

	return nil
}
