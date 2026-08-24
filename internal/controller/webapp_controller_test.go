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
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1 "github.com/Mampiz/webapp-operator/api/v1"
)

const (
	// testNamespace is the namespace the test resources live in.
	testNamespace = "default"
	// testImage is a pinned image that satisfies the operator's tag policy.
	testImage = "nginx:1.27"
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

// key builds the NamespacedName of a child resource in the test namespace.
func key(name string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: testNamespace}
}

var _ = Describe("WebApp Controller", func() {
	ctx := context.Background()

	Context("When reconciling a WebApp without autoscaling", func() {
		const name = "test-webapp"
		webappKey := key(name)

		BeforeEach(func() {
			By("creating a valid WebApp resource")
			webapp := &platformv1.WebApp{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: platformv1.WebAppSpec{
					Image:    testImage,
					Replicas: ptr.To(int32(3)),
					Port:     8080,
				},
			}
			Expect(k8sClient.Create(ctx, webapp)).To(Succeed())
		})

		AfterEach(func() {
			By("deleting the WebApp resource")
			webapp := &platformv1.WebApp{}
			Expect(k8sClient.Get(ctx, webappKey, webapp)).To(Succeed())
			Expect(k8sClient.Delete(ctx, webapp)).To(Succeed())
		})

		It("creates a Deployment that matches the spec", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key(name+"-deployment"), deployment)).To(Succeed())

			Expect(*deployment.Spec.Replicas).To(Equal(int32(3)))
			container := deployment.Spec.Template.Spec.Containers[0]
			Expect(container.Image).To(Equal(testImage))
			Expect(container.Ports[0].ContainerPort).To(Equal(int32(8080)))
		})

		It("hardens the pod with defaults that are safe for any image", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key(name+"-deployment"), deployment)).To(Succeed())

			podSpec := deployment.Spec.Template.Spec
			Expect(podSpec.SecurityContext.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault))

			sc := podSpec.Containers[0].SecurityContext
			Expect(*sc.AllowPrivilegeEscalation).To(BeFalse())
			Expect(sc.Capabilities.Drop).To(ContainElement(corev1.Capability("ALL")))
			// Image-dependent hardening stays opt-in.
			Expect(sc.RunAsNonRoot).To(BeNil())
			Expect(sc.ReadOnlyRootFilesystem).To(BeNil())
		})

		It("defaults the readiness probe to a TCP check on the spec port", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key(name+"-deployment"), deployment)).To(Succeed())

			probe := deployment.Spec.Template.Spec.Containers[0].ReadinessProbe
			Expect(probe).NotTo(BeNil())
			Expect(probe.TCPSocket).NotTo(BeNil())
			Expect(probe.TCPSocket.Port).To(Equal(intstr.FromInt32(8080)))
			Expect(probe.HTTPGet).To(BeNil())
		})

		It("creates a Service that matches the spec", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			service := &corev1.Service{}
			Expect(k8sClient.Get(ctx, key(name+"-service"), service)).To(Succeed())

			Expect(service.Spec.Ports[0].Port).To(Equal(int32(8080)))
			Expect(service.Spec.Selector).To(HaveKeyWithValue("app", name))
		})

		It("does not create an HPA or a PDB when they are not requested", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			hpa := &autoscalingv2.HorizontalPodAutoscaler{}
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(name+"-autoscaler"), hpa))).To(BeTrue())

			pdb := &policyv1.PodDisruptionBudget{}
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(name+"-pdb"), pdb))).To(BeTrue())
		})

		It("reports status conditions and observedGeneration", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			webapp := &platformv1.WebApp{}
			Expect(k8sClient.Get(ctx, webappKey, webapp)).To(Succeed())

			Expect(webapp.Status.ObservedGeneration).To(Equal(webapp.Generation))
			for _, condition := range []string{
				platformv1.ConditionAvailable,
				platformv1.ConditionProgressing,
				platformv1.ConditionDegraded,
			} {
				found := false
				for _, c := range webapp.Status.Conditions {
					if c.Type == condition {
						found = true
						Expect(c.ObservedGeneration).To(Equal(webapp.Generation))
						Expect(c.Reason).NotTo(BeEmpty())
					}
				}
				Expect(found).To(BeTrue(), "expected condition %s to be set", condition)
			}
		})

		It("is idempotent: a second reconcile reports no changes", func() {
			r := newReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key(name+"-deployment"), deployment)).To(Succeed())
			generation := deployment.Generation

			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, key(name+"-deployment"), deployment)).To(Succeed())
			Expect(deployment.Generation).To(Equal(generation), "the Deployment should not be rewritten")
		})
	})

	Context("When reconciling a WebApp with autoscaling", func() {
		const name = "test-webapp-hpa"
		webappKey := key(name)

		BeforeEach(func() {
			By("creating a WebApp with an autoscaling block and a CPU request")
			webapp := &platformv1.WebApp{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: platformv1.WebAppSpec{
					Image:    testImage,
					Replicas: ptr.To(int32(2)),
					Port:     8080,
					Autoscaling: &platformv1.AutoscalingSpec{
						MinReplicas:         2,
						MaxReplicas:         5,
						CPUThresholdPercent: 70,
					},
					Resources: &corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
					},
				},
			}
			Expect(k8sClient.Create(ctx, webapp)).To(Succeed())
		})

		AfterEach(func() {
			webapp := &platformv1.WebApp{}
			Expect(k8sClient.Get(ctx, webappKey, webapp)).To(Succeed())
			Expect(k8sClient.Delete(ctx, webapp)).To(Succeed())
		})

		It("creates an HPA that matches the autoscaling spec", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			hpa := &autoscalingv2.HorizontalPodAutoscaler{}
			Expect(k8sClient.Get(ctx, key(name+"-autoscaler"), hpa)).To(Succeed())

			Expect(*hpa.Spec.MinReplicas).To(Equal(int32(2)))
			Expect(hpa.Spec.MaxReplicas).To(Equal(int32(5)))
			Expect(hpa.Spec.ScaleTargetRef.Name).To(Equal(name + "-deployment"))
		})

		It("propagates the CPU request the HPA needs to compute utilization", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key(name+"-deployment"), deployment)).To(Succeed())

			cpu := deployment.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
			Expect(cpu.String()).To(Equal("100m"))
		})

		It("injects the CPU request the HPA needs even without the webhooks", func() {
			By("creating a WebApp with autoscaling and no resources at all")
			bare := &platformv1.WebApp{
				ObjectMeta: metav1.ObjectMeta{Name: "no-webhook", Namespace: testNamespace},
				Spec: platformv1.WebAppSpec{
					Image: testImage,
					Port:  8080,
					Autoscaling: &platformv1.AutoscalingSpec{
						MinReplicas: 2, MaxReplicas: 5, CPUThresholdPercent: 70,
					},
				},
			}
			Expect(k8sClient.Create(ctx, bare)).To(Succeed())
			defer func() {
				Expect(k8sClient.Delete(ctx, bare)).To(Succeed())
			}()

			// envtest runs no admission webhooks here, which is exactly the
			// situation of a Helm install with webhook.enable=false.
			Expect(bare.Spec.Resources).To(BeNil(), "precondition: no webhook defaulting happened")

			_, err := newReconciler().Reconcile(ctx, reconcile.Request{
				NamespacedName: key("no-webhook"),
			})
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key("no-webhook-deployment"), deployment)).To(Succeed())

			cpu := deployment.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
			Expect(cpu.String()).To(Equal("100m"),
				"the reconciler must not depend on admission having run")
		})

		It("does not inject a CPU request when autoscaling is disabled", func() {
			deployment := &appsv1.Deployment{}
			plain := &platformv1.WebApp{
				ObjectMeta: metav1.ObjectMeta{Name: "no-autoscaling", Namespace: testNamespace},
				Spec:       platformv1.WebAppSpec{Image: testImage, Port: 8080},
			}
			Expect(k8sClient.Create(ctx, plain)).To(Succeed())
			defer func() {
				Expect(k8sClient.Delete(ctx, plain)).To(Succeed())
			}()

			_, err := newReconciler().Reconcile(ctx, reconcile.Request{
				NamespacedName: key("no-autoscaling"),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, key("no-autoscaling-deployment"), deployment)).To(Succeed())
			Expect(deployment.Spec.Template.Spec.Containers[0].Resources.Requests).To(BeEmpty())
		})

		It("leaves the replica count to the HPA", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key(name+"-deployment"), deployment)).To(Succeed())

			By("simulating the HPA scaling the Deployment up")
			deployment.Spec.Replicas = ptr.To(int32(4))
			Expect(k8sClient.Update(ctx, deployment)).To(Succeed())

			_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, key(name+"-deployment"), deployment)).To(Succeed())
			Expect(*deployment.Spec.Replicas).To(Equal(int32(4)), "the operator must not fight the HPA")
		})

		It("deletes the HPA when autoscaling is removed from the spec", func() {
			r := newReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, key(name+"-autoscaler"), &autoscalingv2.HorizontalPodAutoscaler{})).To(Succeed())

			By("removing the autoscaling block")
			webapp := &platformv1.WebApp{}
			Expect(k8sClient.Get(ctx, webappKey, webapp)).To(Succeed())
			webapp.Spec.Autoscaling = nil
			Expect(k8sClient.Update(ctx, webapp)).To(Succeed())

			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			hpa := &autoscalingv2.HorizontalPodAutoscaler{}
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(name+"-autoscaler"), hpa))).To(BeTrue())
		})
	})

	Context("When reconciling a WebApp with optional extras", func() {
		const name = "test-webapp-extras"
		webappKey := key(name)

		BeforeEach(func() {
			webapp := &platformv1.WebApp{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
				Spec: platformv1.WebAppSpec{
					Image:         testImage,
					Replicas:      ptr.To(int32(3)),
					Port:          8080,
					ReadinessPath: "/healthz",
					Security: &platformv1.SecuritySpec{
						RunAsNonRoot:           ptr.To(true),
						ReadOnlyRootFilesystem: ptr.To(true),
					},
					PodDisruptionBudget: &platformv1.PodDisruptionBudgetSpec{
						MinAvailable: ptr.To(intstr.FromInt32(2)),
					},
				},
			}
			Expect(k8sClient.Create(ctx, webapp)).To(Succeed())
		})

		AfterEach(func() {
			webapp := &platformv1.WebApp{}
			Expect(k8sClient.Get(ctx, webappKey, webapp)).To(Succeed())
			Expect(k8sClient.Delete(ctx, webapp)).To(Succeed())
		})

		It("uses an HTTP readiness probe when a path is given", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key(name+"-deployment"), deployment)).To(Succeed())

			probe := deployment.Spec.Template.Spec.Containers[0].ReadinessProbe
			Expect(probe.HTTPGet).NotTo(BeNil())
			Expect(probe.HTTPGet.Path).To(Equal("/healthz"))
			Expect(probe.TCPSocket).To(BeNil())
		})

		It("applies the opt-in security hardening", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, key(name+"-deployment"), deployment)).To(Succeed())

			sc := deployment.Spec.Template.Spec.Containers[0].SecurityContext
			Expect(*sc.RunAsNonRoot).To(BeTrue())
			Expect(*sc.ReadOnlyRootFilesystem).To(BeTrue())
		})

		It("creates a PodDisruptionBudget selecting the WebApp pods", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: webappKey})
			Expect(err).NotTo(HaveOccurred())

			pdb := &policyv1.PodDisruptionBudget{}
			Expect(k8sClient.Get(ctx, key(name+"-pdb"), pdb)).To(Succeed())

			Expect(pdb.Spec.MinAvailable.IntValue()).To(Equal(2))
			Expect(pdb.Spec.Selector.MatchLabels).To(HaveKeyWithValue("app", name))
		})
	})

	Context("When the WebApp does not exist", func() {
		It("returns without error", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: key("missing")})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
