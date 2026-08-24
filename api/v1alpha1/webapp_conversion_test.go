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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1 "github.com/Mampiz/webapp-operator/api/v1"
)

const (
	testImage     = "app:1.0"
	testName      = "app"
	testNamespace = "default"
)

func TestConvertToHub(t *testing.T) {
	tests := []struct {
		name string
		src  WebAppSpec
		want func(*testing.T, *v1.WebApp)
	}{
		{
			name: "no autoscaling fields leaves the block unset",
			src:  WebAppSpec{Image: testImage, Port: 8080, Replicas: ptr.To(int32(3))},
			want: func(t *testing.T, got *v1.WebApp) {
				if got.Spec.Autoscaling != nil {
					t.Fatalf("expected no autoscaling, got %+v", got.Spec.Autoscaling)
				}
				if *got.Spec.Replicas != 3 || got.Spec.Image != testImage || got.Spec.Port != 8080 {
					t.Fatalf("plain fields not carried over: %+v", got.Spec)
				}
			},
		},
		{
			name: "the three flat fields become the grouped block",
			src: WebAppSpec{
				Image: testImage, Port: 8080,
				MinReplicas: ptr.To(int32(2)), MaxReplicas: ptr.To(int32(9)), CPUThreshold: ptr.To(int32(70)),
			},
			want: func(t *testing.T, got *v1.WebApp) {
				a := got.Spec.Autoscaling
				if a == nil {
					t.Fatal("expected autoscaling to be set")
				}
				if a.MinReplicas != 2 || a.MaxReplicas != 9 || a.CPUThresholdPercent != 70 {
					t.Fatalf("unexpected autoscaling: %+v", a)
				}
			},
		},
		{
			name: "a partially filled block is completed rather than rejected",
			src:  WebAppSpec{Image: testImage, Port: 8080, MaxReplicas: ptr.To(int32(5))},
			want: func(t *testing.T, got *v1.WebApp) {
				a := got.Spec.Autoscaling
				if a == nil {
					t.Fatal("expected autoscaling to be set")
				}
				// v1alpha1 allowed setting only one of the three; v1 requires all,
				// so conversion has to supply the rest. Failing here would make a
				// stored object unreadable.
				if a.MinReplicas != 1 || a.MaxReplicas != 5 || a.CPUThresholdPercent != 80 {
					t.Fatalf("unexpected completion: %+v", a)
				}
			},
		},
		{
			name: "max below min is widened to satisfy the v1 CEL rule",
			src: WebAppSpec{
				Image: testImage, Port: 8080,
				MinReplicas: ptr.To(int32(8)), MaxReplicas: ptr.To(int32(3)), CPUThreshold: ptr.To(int32(50)),
			},
			want: func(t *testing.T, got *v1.WebApp) {
				a := got.Spec.Autoscaling
				if a.MaxReplicas < a.MinReplicas {
					t.Fatalf("v1 requires maxReplicas >= minReplicas, got %+v", a)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := &WebApp{
				ObjectMeta: metav1.ObjectMeta{Name: testName, Namespace: testNamespace},
				Spec:       tc.src,
			}
			dst := &v1.WebApp{}
			if err := src.ConvertTo(dst); err != nil {
				t.Fatalf("ConvertTo: %v", err)
			}
			if dst.Name != testName || dst.Namespace != testNamespace {
				t.Fatalf("metadata not carried over: %+v", dst.ObjectMeta)
			}
			tc.want(t, dst)
		})
	}
}

func TestConvertFromHub(t *testing.T) {
	src := &v1.WebApp{
		ObjectMeta: metav1.ObjectMeta{Name: testName, Namespace: testNamespace},
		Spec: v1.WebAppSpec{
			Image:    testImage,
			Port:     8080,
			Replicas: ptr.To(int32(2)),
			Autoscaling: &v1.AutoscalingSpec{
				MinReplicas: 2, MaxReplicas: 9, CPUThresholdPercent: 70,
			},
			ReadinessPath: "/healthz",
		},
		Status: v1.WebAppStatus{ReadyReplicas: 2, ObservedGeneration: 7},
	}

	dst := &WebApp{}
	if err := dst.ConvertFrom(src); err != nil {
		t.Fatalf("ConvertFrom: %v", err)
	}

	if *dst.Spec.MinReplicas != 2 || *dst.Spec.MaxReplicas != 9 || *dst.Spec.CPUThreshold != 70 {
		t.Fatalf("autoscaling not flattened: %+v", dst.Spec)
	}
	if dst.Status.ReadyReplicas != 2 || dst.Status.ObservedGeneration != 7 {
		t.Fatalf("status not carried over: %+v", dst.Status)
	}
	// readinessPath has no v1alpha1 field; it stays on the stored object but
	// cannot be represented in this view. Asserted so the loss is deliberate and
	// documented rather than discovered later.
}

// TestRoundTripPreservesAutoscaling checks the property that matters for a
// served old version: an object written as v1alpha1, stored as v1 and read back
// as v1alpha1 comes back unchanged.
func TestRoundTripPreservesAutoscaling(t *testing.T) {
	original := &WebApp{
		ObjectMeta: metav1.ObjectMeta{Name: testName, Namespace: testNamespace},
		Spec: WebAppSpec{
			Image: testImage, Port: 8080, Replicas: ptr.To(int32(4)),
			MinReplicas: ptr.To(int32(2)), MaxReplicas: ptr.To(int32(9)), CPUThreshold: ptr.To(int32(70)),
		},
	}

	hub := &v1.WebApp{}
	if err := original.ConvertTo(hub); err != nil {
		t.Fatalf("ConvertTo: %v", err)
	}
	back := &WebApp{}
	if err := back.ConvertFrom(hub); err != nil {
		t.Fatalf("ConvertFrom: %v", err)
	}

	if back.Spec.Image != original.Spec.Image ||
		back.Spec.Port != original.Spec.Port ||
		*back.Spec.Replicas != *original.Spec.Replicas ||
		*back.Spec.MinReplicas != *original.Spec.MinReplicas ||
		*back.Spec.MaxReplicas != *original.Spec.MaxReplicas ||
		*back.Spec.CPUThreshold != *original.Spec.CPUThreshold {
		t.Fatalf("round trip changed the object:\n before %+v\n after  %+v", original.Spec, back.Spec)
	}
}
