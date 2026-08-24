# Observability

The operator reports on **itself and on the workloads it manages**. That
distinction matters: a controller that only exposes its own reconcile counters
tells you the operator is healthy, not that the applications are.

![Grafana dashboard](assets/grafana.png)

## Metrics

Served on the manager's `/metrics` endpoint, protected by authentication and
authorization by default.

### Operator metrics

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `webapp_reconcile_total` | counter | `result` | Reconciles, by outcome (`success`, `error`). Incremented exactly once per reconcile. |
| `webapp_child_operations_total` | counter | `resource`, `operation` | Writes to managed resources. `resource` is `deployment`/`service`/`hpa`/`pdb`; `operation` is `created`/`updated`. |

`webapp_child_operations_total` only moves when something actually changed. In a
steady state it is flat — a continuously rising `updated` count means the
operator is rewriting a resource on every pass, which is a bug worth chasing
(see [Troubleshooting](troubleshooting.md#the-deployment-is-rewritten-in-a-loop)).

### Operand metrics

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `webapp_ready_replicas` | gauge | `namespace`, `name` | Pods currently ready behind each `WebApp`. |
| `webapp_info` | gauge | `namespace`, `name`, `image` | Always `1`; carries the running image so it can be joined onto other series. |

Series for a deleted `WebApp` are removed on the reconcile that observes the
deletion, and `webapp_info` drops the previous series when the image changes, so
dashboards never show workloads or image versions that no longer exist.

Cardinality is bounded by the number of `WebApp` objects, which is small and
operator-controlled — unlike per-pod or per-request labels.

### Inherited controller-runtime metrics

`controller_runtime_reconcile_time_seconds`,
`controller_runtime_reconcile_errors_total`, `workqueue_depth`,
`workqueue_adds_total` and the rest come for free with the manager and are worth
the same attention as the custom ones.

## Scraping

With the Prometheus Operator, enable the bundled `ServiceMonitor`:

```bash
helm upgrade webapp-operator ./dist/chart --set prometheus.enable=true
```

or apply `config/prometheus/` with kustomize.

To scrape by hand, point Prometheus at the metrics service in
`webapp-operator-system`. For a quick local look, expose the endpoint in plain
HTTP — acceptable on a throwaway cluster, not in production:

```bash
kubectl port-forward -n webapp-operator-system \
  service/webapp-operator-controller-manager-metrics-service 8443:8443
```

## Grafana dashboard

[`config/grafana/webapp-operator-dashboard.json`](../config/grafana/webapp-operator-dashboard.json)
— import it and pick your Prometheus data source.

| Panel | Query | Answers |
|---|---|---|
| Reconciles per second | `sum by (result) (rate(webapp_reconcile_total[5m]))` | Is the controller working, and is it failing? |
| Reconcile error ratio | error rate ÷ total rate | How bad is it, independent of scale? |
| Child operations per second | `sum by (resource, operation) (rate(webapp_child_operations_total[5m]))` | What is the operator changing right now? |
| Reconcile latency p95 | `histogram_quantile(0.95, …reconcile_time_seconds_bucket…)` | Are reconciles slowing down? |
| Child operations totals | `sum by (resource, operation) (webapp_child_operations_total)` | How much has it done overall? |

The last panel is a table on purpose. `rate()` answers "what is happening now",
so a one-off burst of `created` operations decays to zero within minutes and
becomes invisible; the cumulative table keeps those low-frequency events legible
next to a counter that may be orders of magnitude larger.

## Alerts

[`config/prometheus/alerts.yaml`](../config/prometheus/alerts.yaml) ships three
rules. Thresholds are derived from how the controller behaves, not from round
numbers: controller-runtime retries failures with exponential backoff, so a
transient API error resolves itself within seconds and must not page anyone.

| Alert | Fires when | Why that threshold |
|---|---|---|
| `WebAppOperatorReconcileErrors` | > 10% of reconciles failing for 10m | Long enough that the retry budget would have absorbed a blip; a ratio stays valid as the number of WebApps grows. |
| `WebAppUnavailable` | A `WebApp` has zero ready replicas for 15m | Longer than a normal rolling update plus image pull, so healthy deploys never trip it. |
| `WebAppOperatorWorkQueueBacklog` | Queue depth > 10 for 15m | Well above the transient spike from creating many WebApps at once; sustained depth means desired state is not being applied. |

## Events

The operator emits Kubernetes events on meaningful changes only — a steady-state
reconcile is silent, so `kubectl describe` stays readable.

```console
$ kubectl describe webapp my-api
...
Events:
  Type     Reason                Age   From               Message
  ----     ------                ----  ----               -------
  Normal   DeploymentReconciled  2m    webapp-controller  Deployment my-api-deployment created
  Normal   ServiceReconciled     2m    webapp-controller  Service my-api-service created
  Normal   HPAReconciled         2m    webapp-controller  HorizontalPodAutoscaler my-api-autoscaler created
```

Failures produce a `Warning` with reason `ReconcileFailed` and set the
`Degraded` condition, whose message carries the underlying error.

## Reproducing the dashboard screenshot

The image at the top of this page is generated, not hand-captured. See
[Recording the media](media.md).
