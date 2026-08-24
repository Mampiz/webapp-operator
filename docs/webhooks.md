# Admission webhooks

The operator registers two admission webhooks. They run inside the API server's
request path, **before** an object is persisted, which is what makes them the
right place for policy: a rejected `WebApp` never exists, so no controller ever
has to cope with it.

```
kubectl apply
   │
   ▼
API server
   ├─ authentication / authorization
   ├─ MUTATING   admission  ──►  defaulting webhook   (may modify the object)
   ├─ schema validation (OpenAPI + CEL rules from the CRD)
   ├─ VALIDATING admission  ──►  validating webhook   (may only accept/reject)
   ▼
  etcd
```

Mutating runs first so the object is validated **after** its defaults are
applied — what gets stored is always a complete, valid object.

## Defaulting webhook

Fills in what makes a submitted `WebApp` coherent.

| Rule | Behaviour |
|---|---|
| `app.kubernetes.io/managed-by` label | Set to `webapp-operator` when absent. A label the user set is never overwritten. |
| `spec.replicas` | Defaults to `1` when omitted. An explicit `0` is preserved. |
| CPU request | When `spec.autoscaling` is set and no CPU request exists, injects `100m`. |

The CPU request rule is the one that matters most. HPA target utilization is a
percentage **of the container's CPU request**; with no request the autoscaler
cannot compute a ratio, reports `<unknown>` and never scales. Rather than let
users create an autoscaler that is silently inert, the webhook makes the
resource self-consistent. `100m` is small enough not to waste quota and large
enough to produce a meaningful ratio.

## Validating webhook

Rejects image references that are not reproducible.

| Reference | Verdict |
|---|---|
| `my-api:1.4.0` | ✅ accepted |
| `my-api@sha256:9f2c…` | ✅ accepted — digests are immutable |
| `registry.internal:5000/team/api:1.4.0` | ✅ accepted — the registry port is not a tag |
| `my-api:latest` | ❌ rejected — mutable tag |
| `my-api` | ❌ rejected — an untagged reference means `latest` |

```console
$ kubectl apply -f config/samples/platform_v1_webapp_invalid.yaml
Error from server (Forbidden): admission webhook "vwebapp-v1.kb.io" denied the request:
spec.image "nginx:latest" uses the mutable "latest" tag; pin an explicit version for reproducibility
```

The rule applies on create **and** update, so an existing `WebApp` cannot be
edited into a mutable tag. Deletion is never blocked.

### Why this policy

A mutable tag breaks the link between a manifest and what actually runs. The
same `WebApp` spec can run different code tomorrow than it does today, which
means a rollback to a known-good manifest is not guaranteed to restore
known-good behaviour, and two clusters applying the same manifest can end up
running different builds. Pinning a tag or a digest is what makes a deployment
reproducible and an incident timeline trustworthy.

The operator holds itself to the same rule: its own release pipeline publishes
`1.0.0` and `1.0`, but deliberately **no `latest` tag**.

## Running the webhooks

Webhooks are served over TLS by the manager on port 9443. The API server must
trust that certificate, which is why cert-manager is a prerequisite. See
[Installation](installation.md#enabling-the-admission-webhooks).

They are **optional**. Without them the operator still reconciles correctly:

- The controller does not assume defaults were applied — a `WebApp` with no
  `spec.replicas` is still treated as 1 replica.
- Schema validation (ranges, required fields, the `maxReplicas >= minReplicas`
  CEL rule) is part of the CRD and always applies.
- What you lose is the image-tag policy and the automatic CPU request.

## Local development

`make run` runs the manager outside the cluster, where it has no serving
certificate, so webhooks must be disabled:

```bash
ENABLE_WEBHOOKS=false make run
```

The Helm chart sets the same variable from `webhook.enable`, which is why the
chart installs cleanly on a cluster without cert-manager.

## Failure policy

Both webhooks use `failurePolicy: fail`: if the webhook endpoint is unreachable,
the API server **rejects** the request rather than admitting it unchecked. That
is the safe default for a policy webhook — an operator outage must not become a
silent policy bypass — but it does mean `WebApp` writes are unavailable while
the manager is down. `WebApp` objects that already exist keep running; only
creates and updates are blocked.

## Testing

The webhook logic is covered by unit tests against the defaulter and validator
(100% statement coverage), and the suite runs a real API server through
[envtest](https://book.kubebuilder.io/reference/envtest.html), which provisions
the serving certificates itself — no cert-manager needed in CI.

```bash
make test
```
