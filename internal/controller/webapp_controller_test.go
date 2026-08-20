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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1 "github.com/Mampiz/webapp-operator/api/v1"
)

// newReconciler builds a WebAppReconciler wired for tests. It uses the envtest
// client and a fake event recorder (so r.Recorder.Event does not panic).
func newReconciler() *WebAppReconciler {
	return &WebAppReconciler{
		Client:   k8sClient,
		Scheme:   k8sClient.Scheme(),
		Recorder: record.NewFakeRecorder(100),
	}
}

var _ = Describe("WebApp Controller", func() {
	ctx := context.Background()

	Context("When reconciling a WebApp without autoscaling", func() {
		const name = "test-webapp"
		key := types.NamespacedName{Name: name, Namespace: "default"}

		BeforeEach(func() {
			By("creating a valid WebApp resource")
			webapp := &platformv1.WebApp{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: platformv1.WebAppSpec{
					Image:    "nginx:latest",
					Replicas: 3,
					Port:     8080,
				},
			}
			Expect(k8sClient.Create(ctx, webapp)).To(Succeed())
		})

		AfterEach(func() {
			By("deleting the WebApp resource")
			webapp := &platformv1.WebApp{}
			Expect(k8sClient.Get(ctx, key, webapp)).To(Succeed())
			Expect(k8sClient.Delete(ctx, webapp)).To(Succeed())
		})

		It("creates a Deployment that matches the spec", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-deployment", Namespace: "default",
			}, deployment)).To(Succeed())

			Expect(*deployment.Spec.Replicas).To(Equal(int32(3)))
			container := deployment.Spec.Template.Spec.Containers[0]
			Expect(container.Image).To(Equal("nginx:latest"))
			Expect(container.Ports[0].ContainerPort).To(Equal(int32(8080)))
		})

		It("creates a Service that matches the spec", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			service := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-service", Namespace: "default",
			}, service)).To(Succeed())

			Expect(service.Spec.Ports[0].Port).To(Equal(int32(8080)))
			Expect(service.Spec.Selector).To(HaveKeyWithValue("app", name))
		})

		It("does not create an HPA when autoscaling is disabled", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			hpa := &autoscalingv2.HorizontalPodAutoscaler{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-autoscaler", Namespace: "default",
			}, hpa)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When reconciling a WebApp with autoscaling", func() {
		const name = "test-webapp-hpa"
		key := types.NamespacedName{Name: name, Namespace: "default"}

		BeforeEach(func() {
			By("creating a WebApp with an autoscaling block")
			webapp := &platformv1.WebApp{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: platformv1.WebAppSpec{
					Image:    "nginx:latest",
					Replicas: 2,
					Port:     8080,
					Autoscaling: &platformv1.AutoscalingSpec{
						MinReplicas:         2,
						MaxReplicas:         5,
						CPUThresholdPercent: 70,
					},
				},
			}
			Expect(k8sClient.Create(ctx, webapp)).To(Succeed())
		})

		AfterEach(func() {
			webapp := &platformv1.WebApp{}
			Expect(k8sClient.Get(ctx, key, webapp)).To(Succeed())
			Expect(k8sClient.Delete(ctx, webapp)).To(Succeed())
		})

		It("creates an HPA that matches the autoscaling spec", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			hpa := &autoscalingv2.HorizontalPodAutoscaler{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-autoscaler", Namespace: "default",
			}, hpa)).To(Succeed())

			Expect(*hpa.Spec.MinReplicas).To(Equal(int32(2)))
			Expect(hpa.Spec.MaxReplicas).To(Equal(int32(5)))
			Expect(hpa.Spec.ScaleTargetRef.Name).To(Equal(name + "-deployment"))
		})
	})
})
