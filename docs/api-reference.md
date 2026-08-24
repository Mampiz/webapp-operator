# API reference

**Group** `platform.miportfolio.com` · **Version** `v1` · **Kind** `WebApp` ·
**Scope** Namespaced

```yaml
apiVersion: platform.miportfolio.com/v1
kind: WebApp
metadata:
  name: my-api
spec:
  image: my-api:v1.2.3
  port: 8080
  replicas: 2
```

The live schema is always authoritative:

```bash
kubectl explain webapp.spec --recursive
```

## `spec`

| Field | Type | Required | Default | Description |
|---|---|:--:|---|---|
| `image` | string | ✅ | — | Container image. Must carry an explicit tag or a digest. |
| `port` | int32 | ✅ | — | Port the application listens on. `1`–`65535`. |
| `replicas` | int32 | ➖ | `1` | Desired replicas. **Ignored when `autoscaling` is set.** Minimum `0`. |
| `readinessPath` | string | ➖ | — | HTTP path probed on `port`. Must start with `/`. Without it, a TCP check is used. |
| `resources` | [ResourceRequirements](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#resources) | ➖ | — | Container `requests` / `limits`. |
| `autoscaling` | [AutoscalingSpec](#autoscalingspec) | ➖ | — | Creates a HorizontalPodAutoscaler. |
| `security` | [SecuritySpec](#securityspec) | ➖ | — | Image-dependent pod hardening. |
| `podDisruptionBudget` | [PodDisruptionBudgetSpec](#poddisruptionbudgetspec) | ➖ | — | Creates a PodDisruptionBudget. |

### Validation applied to `image`

Two independent layers:

1. **Schema** — must match a plausible image reference, including registries
   with a port (`registry.internal:5000/team/api:1.4.0`).
2. **Admission webhook** — rejects `latest` and untagged references. Digests
   (`app@sha256:…`) are accepted. See [Admission webhooks](webhooks.md).

The webhook layer only runs where webhooks are installed; the schema layer
always applies.

### AutoscalingSpec

| Field | Type | Required | Description |
|---|---|:--:|---|
| `minReplicas` | int32 | ✅ | Lower bound. Minimum `1`. |
| `maxReplicas` | int32 | ✅ | Upper bound. Minimum `1`, and must be `>= minReplicas`. |
| `cpuThresholdPercent` | int32 | ✅ | Target average CPU utilization, `1`–`100`. |

`maxReplicas >= minReplicas` is enforced by a CEL rule on the object, so an
inconsistent pair is rejected by the API server before the controller sees it.

**CPU utilization is a percentage of the container's CPU request.** Without a
request the autoscaler cannot compute a ratio and never scales, so when
`autoscaling` is set and no CPU request is given the defaulting webhook injects
`100m`. On a cluster without the webhooks, set `spec.resources.requests.cpu`
yourself.

Autoscaling also requires a metrics source in the cluster, typically
[metrics-server](https://github.com/kubernetes-sigs/metrics-server).

### SecuritySpec

Hardening that is safe for any image is **always applied and not configurable**:
all capabilities dropped, `allowPrivilegeEscalation: false`, and the
`RuntimeDefault` seccomp profile. The fields below are the image-dependent parts.

| Field | Type | Description |
|---|---|---|
| `runAsNonRoot` | bool | Refuse to start the container if the image would run as root. |
| `runAsUser` | int64 | UID to run as. Minimum `1`. |
| `readOnlyRootFilesystem` | bool | Mount the container root filesystem read-only. |

> Because all capabilities are dropped, a container cannot bind a port below
> 1024 — that requires `CAP_NET_BIND_SERVICE`. Use a port such as 8080, or an
> image built to listen on one.

### PodDisruptionBudgetSpec

| Field | Type | Description |
|---|---|---|
| `minAvailable` | int or string | Pods that must stay available, e.g. `1` or `"50%"`. |
| `maxUnavailable` | int or string | Pods that may be unavailable. |

Exactly one of the two must be set; a CEL rule rejects both or neither.

## `status`

Written by the operator only. Never set these fields by hand.

| Field | Type | Description |
|---|---|---|
| `readyReplicas` | int32 | Pods currently passing their readiness probe. |
| `observedGeneration` | int64 | The `metadata.generation` this status was computed from. |
| `conditions` | []Condition | Standard Kubernetes conditions, listed below. |

### Conditions

| Type | `True` when | Reasons |
|---|---|---|
| `Available` | Every desired replica is ready | `DeploymentReady`, `DeploymentNotReady` |
| `Progressing` | The Deployment is still rolling out | `RolloutInProgress`, `RolloutComplete` |
| `Degraded` | A managed resource failed to reconcile | `<Kind>ReconcileFailed`, `ReconcileSucceeded` |

Each condition carries the `observedGeneration` it was computed from, so clients
can distinguish "available for the spec I just submitted" from "available, but
for the previous spec".

```console
$ kubectl get webapp my-api -o jsonpath='{range .status.conditions[*]}{.type}={.status} ({.reason}){"\n"}{end}'
Available=True (DeploymentReady)
Progressing=False (RolloutComplete)
Degraded=False (ReconcileSucceeded)
```

## Resources created

For a `WebApp` named `my-api`:

| Resource | Name | Created |
|---|---|---|
| Deployment | `my-api-deployment` | Always |
| Service (ClusterIP) | `my-api-service` | Always |
| HorizontalPodAutoscaler | `my-api-autoscaler` | When `autoscaling` is set |
| PodDisruptionBudget | `my-api-pdb` | When `podDisruptionBudget` is set |

All of them carry an owner reference to the `WebApp`, so deleting it removes
them. Removing an optional block (`autoscaling`, `podDisruptionBudget`) deletes
the corresponding object on the next reconcile.

Pods are labelled `app: <webapp-name>`, which is what the Deployment selector,
the Service selector and the PodDisruptionBudget selector all match on.

## Full example

```yaml
apiVersion: platform.miportfolio.com/v1
kind: WebApp
metadata:
  name: my-api
spec:
  image: nginxinc/nginx-unprivileged:1.27-alpine
  port: 8080
  readinessPath: /
  resources:
    requests:
      cpu: 100m
      memory: 64Mi
    limits:
      memory: 128Mi
  security:
    runAsNonRoot: true
  autoscaling:
    minReplicas: 2
    maxReplicas: 10
    cpuThresholdPercent: 70
  podDisruptionBudget:
    minAvailable: 1
```
