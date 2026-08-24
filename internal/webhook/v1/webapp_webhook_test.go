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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	platformv1 "github.com/Mampiz/webapp-operator/api/v1"
)

// latestImage is a rejected image reference reused across several tests.
const latestImage = "nginx:latest"

var _ = Describe("WebApp Webhook", func() {
	var (
		obj       *platformv1.WebApp
		oldObj    *platformv1.WebApp
		validator WebAppCustomValidator
		defaulter WebAppCustomDefaulter
	)

	BeforeEach(func() {
		obj = &platformv1.WebApp{}
		oldObj = &platformv1.WebApp{}
		validator = WebAppCustomValidator{}
		defaulter = WebAppCustomDefaulter{}
	})

	Context("Defaulting webhook", func() {
		It("sets the managed-by label when it is absent", func() {
			Expect(defaulter.Default(ctx, obj)).To(Succeed())
			Expect(obj.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "webapp-operator"))
		})

		It("does not overwrite a managed-by label the user already set", func() {
			obj.Labels = map[string]string{"app.kubernetes.io/managed-by": "helm"}
			Expect(defaulter.Default(ctx, obj)).To(Succeed())
			Expect(obj.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "helm"))
		})

		It("defaults replicas to 1 when omitted", func() {
			Expect(defaulter.Default(ctx, obj)).To(Succeed())
			Expect(obj.Spec.Replicas).NotTo(BeNil())
			Expect(*obj.Spec.Replicas).To(Equal(int32(1)))
		})

		It("keeps an explicit replica count, including zero", func() {
			obj.Spec.Replicas = ptr.To(int32(0))
			Expect(defaulter.Default(ctx, obj)).To(Succeed())
			Expect(*obj.Spec.Replicas).To(Equal(int32(0)))
		})

		It("injects a CPU request when autoscaling is enabled without one", func() {
			obj.Spec.Autoscaling = &platformv1.AutoscalingSpec{
				MinReplicas: 1, MaxReplicas: 3, CPUThresholdPercent: 70,
			}
			Expect(defaulter.Default(ctx, obj)).To(Succeed())

			cpu := obj.Spec.Resources.Requests[corev1.ResourceCPU]
			Expect(cpu.String()).To(Equal("100m"))
		})

		It("does not override a CPU request the user provided", func() {
			obj.Spec.Autoscaling = &platformv1.AutoscalingSpec{
				MinReplicas: 1, MaxReplicas: 3, CPUThresholdPercent: 70,
			}
			obj.Spec.Resources = &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")},
			}
			Expect(defaulter.Default(ctx, obj)).To(Succeed())

			cpu := obj.Spec.Resources.Requests[corev1.ResourceCPU]
			Expect(cpu.String()).To(Equal("250m"))
		})

		It("does not inject resources when autoscaling is disabled", func() {
			Expect(defaulter.Default(ctx, obj)).To(Succeed())
			Expect(obj.Spec.Resources).To(BeNil())
		})
	})

	Context("Validating webhook", func() {
		It("denies an image using the :latest tag", func() {
			obj.Spec.Image = latestImage
			Expect(validator.ValidateCreate(ctx, obj)).Error().To(HaveOccurred())
		})

		It("denies an image with no tag (implicit latest)", func() {
			obj.Spec.Image = "nginx"
			Expect(validator.ValidateCreate(ctx, obj)).Error().To(HaveOccurred())
		})

		It("admits an image pinned to an explicit version tag", func() {
			obj.Spec.Image = "nginx:1.27"
			Expect(validator.ValidateCreate(ctx, obj)).Error().NotTo(HaveOccurred())
		})

		It("admits an image pinned by digest", func() {
			obj.Spec.Image = "nginx@sha256:aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
			Expect(validator.ValidateCreate(ctx, obj)).Error().NotTo(HaveOccurred())
		})

		It("does not mistake a registry port for a tag", func() {
			obj.Spec.Image = "myregistry:5000/app:1.0"
			Expect(validator.ValidateCreate(ctx, obj)).Error().NotTo(HaveOccurred())

			obj.Spec.Image = "myregistry:5000/app:latest"
			Expect(validator.ValidateCreate(ctx, obj)).Error().To(HaveOccurred())
		})

		It("applies the same rule on update", func() {
			oldObj.Spec.Image = "nginx:1.27"
			obj.Spec.Image = latestImage
			Expect(validator.ValidateUpdate(ctx, oldObj, obj)).Error().To(HaveOccurred())
		})

		It("allows deletion unconditionally", func() {
			obj.Spec.Image = latestImage
			Expect(validator.ValidateDelete(ctx, obj)).Error().NotTo(HaveOccurred())
		})
	})
})
