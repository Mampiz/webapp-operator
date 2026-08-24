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
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	platformv1 "github.com/Mampiz/webapp-operator/api/v1"
)

// Metric label names, factored out so the label sets cannot drift apart.
const (
	labelNamespace = "namespace"
	labelName      = "name"
)

var (
	// reconcileTotal counts WebApp reconcile invocations, labeled by outcome.
	reconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "webapp_reconcile_total",
			Help: "Total number of WebApp reconciles, labeled by result (success|error).",
		},
		[]string{"result"},
	)

	// childOperationsTotal counts create/update operations the operator performs
	// on the resources it manages (Deployment, Service, HPA).
	childOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "webapp_child_operations_total",
			Help: "Total create/update operations on child resources, labeled by resource and operation.",
		},
		[]string{"resource", "operation"},
	)

	// webappReadyReplicas exposes the state of the *operand*, not the operator:
	// how many pods are actually serving behind each WebApp. Cardinality is bounded
	// by the number of WebApp objects, which is a user-controlled but small set.
	webappReadyReplicas = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "webapp_ready_replicas",
			Help: "Number of ready pods behind each WebApp.",
		},
		[]string{labelNamespace, labelName},
	)

	// webappInfo is an info-style metric: always 1, carrying descriptive labels
	// that can be joined onto other series (e.g. to break down by image).
	webappInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "webapp_info",
			Help: "Static information about each WebApp; the value is always 1.",
		},
		[]string{labelNamespace, labelName, "image"},
	)
)

func init() {
	// Register with controller-runtime's registry so the metrics are served on
	// the manager's existing /metrics endpoint.
	metrics.Registry.MustRegister(reconcileTotal, childOperationsTotal, webappReadyReplicas, webappInfo)
}

// recordWebAppMetrics publishes the observed operand state for one WebApp.
func recordWebAppMetrics(webapp *platformv1.WebApp, ready int32) {
	id := prometheus.Labels{labelNamespace: webapp.Namespace, labelName: webapp.Name}
	webappReadyReplicas.With(id).Set(float64(ready))

	// The image is a label, so a rollout would otherwise leave the previous
	// image's series behind forever. Drop this WebApp's series before re-adding.
	webappInfo.DeletePartialMatch(id)
	webappInfo.WithLabelValues(webapp.Namespace, webapp.Name, webapp.Spec.Image).Set(1)
}

// forgetWebAppMetrics removes the series of a WebApp that no longer exists, so
// deleted objects do not stay visible in dashboards and alerts.
func forgetWebAppMetrics(namespace, name string) {
	id := prometheus.Labels{labelNamespace: namespace, labelName: name}
	webappReadyReplicas.DeletePartialMatch(id)
	webappInfo.DeletePartialMatch(id)
}
