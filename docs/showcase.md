# Showcase

What this operator does that a folder of YAML manifests cannot. Every transcript
below is **real output** from `make showcase`, not an illustration.

```bash
make showcase                      # scenarios 1–7, about a minute
SHOWCASE_AUTOSCALING=1 make showcase   # plus live autoscaling under CPU load
```

![Capability showcase](assets/showcase.gif)

---

## 1. Defaulting — a minimal spec is completed into a coherent one

Submitted:

```yaml
apiVersion: platform.miportfolio.com/v1
kind: WebApp
metadata: {name: shop-api}
spec:
  image: nginxinc/nginx-unprivileged:1.27-alpine
  port: 8080
  readinessPath: /
  autoscaling: {minReplicas: 2, maxReplicas: 8, cpuThresholdPercent: 50}
```

Stored:

```text
  replicas:     1
  cpu request:  100m
  managed-by:   webapp-operator
```

Neither `replicas` nor the CPU request was sent. The defaulting webhook added
both — and the CPU request is not cosmetic: HPA target utilization is a
percentage *of the request*, so without one the autoscaler reports `<unknown>`
forever and never scales. The API refuses to store a configuration that cannot
work.

## 2. One resource in, a full stack out

```console
$ kubectl get webapp,deployment,service,hpa --no-headers
webapp.platform.miportfolio.com/shop-api   nginxinc/nginx-unprivileged:1.27-alpine   1   True   11s
deployment.apps/shop-api-deployment   1/1   1   1   11s
service/shop-api-service   ClusterIP   10.96.95.39   <none>   8080/TCP   11s
horizontalpodautoscaler.autoscaling/shop-api-autoscaler   Deployment/shop-api-deployment   cpu: <unknown>/50%   2   8   0   11s
```

One object created three, each owner-referenced back to it, each with the
labels, selectors, probes and security context wired consistently.

## 3. Drift correction — a manual edit does not survive

The scenario every platform team knows: someone fixes production by hand.

```console
$ kubectl set image deployment/shop-api-deployment webapp=nginxinc/nginx-unprivileged:1.26-alpine
deployment.apps/shop-api-deployment image updated
```

Seconds later, without anyone touching the `WebApp`:

```text
  edit applied:  nginxinc/nginx-unprivileged:1.26-alpine
  image now:     nginxinc/nginx-unprivileged:1.27-alpine
```

This is what **level-based** reconciliation buys. The controller does not react
to the edit — it re-derives what the world should look like from the spec and
corrects whatever differs. It works the same whether the change was a `kubectl`
command, a broken script, or something that happened while the operator was
offline.

> The replica count is the deliberate exception when autoscaling is enabled: the
> HPA owns that field, and the operator does not fight it. One writer per field
> — see [Design notes](design.md#the-hpa-owns-specreplicas-when-autoscaling-is-enabled).

## 4. Self-healing — delete what the operator owns

```console
$ kubectl delete deployment/shop-api-deployment service/shop-api-service
deployment.apps "shop-api-deployment" deleted from showcase namespace
service "shop-api-service" deleted from showcase namespace

$ kubectl get deployment,service --no-headers
deployment.apps/shop-api-deployment   0/1   1   0   0s
service/shop-api-service   ClusterIP   10.96.144.236   <none>   8080/TCP   0s
```

Both were already back within a second — the operator watches the resources it
owns, so their deletion is itself the trigger.

## 5. Honest status — a broken image is reported, not hidden

```console
$ kubectl patch webapp shop-api --type=merge -p '{"spec":{"image":"does-not-exist.invalid/nope:1.0"}}'
webapp.platform.miportfolio.com/shop-api patched
```

```text
  Available=False (DeploymentNotReady) 0/2 replicas ready
  Progressing=True (RolloutInProgress) 1/2 replicas updated and ready
  Degraded=False (ReconcileSucceeded) all managed resources reconciled
```

Read those three together — they are saying different things on purpose:

- **Available=False** — no pod is serving. The truth, stated plainly.
- **Progressing=True** — a rollout is under way; this is not a stuck state yet.
- **Degraded=False** — *the operator did its job*. It created exactly the
  Deployment it was asked to. The failure is in the workload, not in the
  reconciliation.

That last distinction is the point of separating the conditions: it tells an
on-call engineer whether to debug the operator or the application.

Rolling back is a spec edit, and the status follows:

```console
$ kubectl patch webapp shop-api --type=merge -p '{"spec":{"image":"nginxinc/nginx-unprivileged:1.27-alpine"}}'
$ kubectl get webapp shop-api
NAME       IMAGE                                     READY   AVAILABLE   AGE
shop-api   nginxinc/nginx-unprivileged:1.27-alpine   2       True        37s
```

## 6. Admission control — four kinds of invalid input, four rejections

Four different mechanisms, all refusing to store the object:

```console
# mutable tag — rejected by the validating webhook
Error from server (Forbidden): admission webhook "vwebapp-v1.kb.io" denied the request:
spec.image "nginx:latest" uses the mutable "latest" tag; pin an explicit version for reproducibility

# port out of range — rejected by the OpenAPI schema
The WebApp "rejected" is invalid: spec.port: Invalid value: 70000:
spec.port in body should be less than or equal to 65535

# maxReplicas < minReplicas — rejected by a CEL rule
The WebApp "rejected" is invalid: spec.autoscaling: Invalid value:
maxReplicas must be greater than or equal to minReplicas

# both PDB fields set — rejected by a CEL rule
The WebApp "rejected" is invalid: spec.podDisruptionBudget: Invalid value:
exactly one of minAvailable or maxUnavailable must be set
```

None of these objects was ever stored, so no controller ever had to handle them.
Invalid states are made unrepresentable rather than defended against — and the
cross-field rules (`maxReplicas >= minReplicas`, exactly one PDB field) are
enforced by the API server itself, with no code running.

## 7. Cascade delete — one delete, nothing left behind

```console
$ kubectl get deployment,service,hpa --no-headers
deployment.apps/shop-api-deployment   2/2   2   2   25s
service/shop-api-service   ClusterIP   10.96.144.236   <none>   8080/TCP   25s
horizontalpodautoscaler.autoscaling/shop-api-autoscaler   Deployment/shop-api-deployment   cpu: <unknown>/50%   2   8   2   37s

$ kubectl delete webapp shop-api
webapp.platform.miportfolio.com "shop-api" deleted from showcase namespace

$ kubectl get deployment,service,hpa --no-headers
No resources found in showcase namespace.
```

No finalizers, no cleanup code, no chance of leaking a resource if the operator
happens to be down at that moment: Kubernetes garbage collection follows the
owner references.

## Bonus — autoscaling under real CPU load

```bash
SHOWCASE_AUTOSCALING=1 make showcase   # as part of the showcase
make record-autoscaling                # or on its own, recorded
```

![Autoscaling under load](assets/autoscaling.gif)

The `WebApp` sets **no resources at all** — only an autoscaling block. The CPU
request it needs is supplied for it:

```console
$ kubectl get deployment shop-api-deployment \
    -o jsonpath='{.spec.template.spec.containers[0].resources.requests.cpu}'
100m
```

Then the pods are driven with genuine CPU load:

```text
  cpu=1%/50%         replicas=2
  cpu=1136%/50%      replicas=2
Utilization reached 1136% of the request; scaled to 4 replicas.
```

```console
$ kubectl describe hpa shop-api-autoscaler
Events:
  Type    Reason             Age   Message
  ----    ------             ----  -------
  Normal  SuccessfulRescale  40s   New size: 2; reason: Current number of replicas below Spec.MinReplicas
  Normal  SuccessfulRescale  7s    New size: 4; reason: cpu resource utilization (percentage of request) above target
```

Read the reason the autoscaler gives for its decision: **"percentage of
request"**. That request is the one nobody typed. Without it the ratio has no
denominator, the HPA reports `<unknown>`, and this scale-up never happens — which
is why the value is applied by the reconciler and not only by the optional
admission webhook.

The operator itself never wrote the replica count during any of this. With
autoscaling enabled that field belongs to the HPA: one writer per field.

---

## Reproducing this

Any cluster with the operator, its webhooks and metrics-server installed:

```bash
make demo        # sets all of that up on a kind cluster
make showcase    # runs the scenarios above
```

The GIF is regenerated with `make record-showcase` — see
[Recording the media](media.md).
