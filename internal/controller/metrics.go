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
)

func init() {
	// Register with controller-runtime's registry so the metrics are served on
	// the manager's existing /metrics endpoint.
	metrics.Registry.MustRegister(reconcileTotal, childOperationsTotal)
}
