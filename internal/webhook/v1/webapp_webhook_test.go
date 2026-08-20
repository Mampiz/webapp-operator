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
