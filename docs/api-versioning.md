# API versioning

`WebApp` is served in two versions. `v1` is the storage version; `v1alpha1` is
still served for compatibility and is converted on the fly.

| Version | Served | Stored | Status |
|---|:--:|:--:|---|
| `platform.miportfolio.com/v1` | ✅ | ✅ | Current |
| `platform.miportfolio.com/v1alpha1` | ✅¹ | ❌ | Deprecated — `kubectl` warns on use |

¹ Only where the conversion webhook is installed. See
[below](#the-webhook-free-install-serves-only-v1).

## What changed between the versions

The autoscaling settings. In `v1alpha1` they were three flat, individually
optional fields:

```yaml
apiVersion: platform.miportfolio.com/v1alpha1
kind: WebApp
spec:
  image: app:1.0
  port: 8080
  minReplicas: 2
  maxReplicas: 9
  cpuThreshold: 70
```

That shape had two problems. There was no way to express "autoscaling is off"
other than leaving all three empty, and nothing prevented a half-filled
combination — `maxReplicas` alone was accepted and meant nothing coherent.

`v1` groups them, so presence means enabled and the schema can require the three
together, plus a cross-field rule:

```yaml
apiVersion: platform.miportfolio.com/v1
kind: WebApp
spec:
  image: app:1.0
  port: 8080
  autoscaling:
    minReplicas: 2
    maxReplicas: 9
    cpuThresholdPercent: 70
```

## Why the old version is still served

**A CRD is a public contract.** Removing a version breaks every manifest, script
and pipeline written against it, and — worse — makes objects already stored under
it unreadable. Deprecating and converting is the compatible path: old clients
keep working while new ones get the better shape, and the migration happens on
someone else's schedule rather than on a deadline set by this repository.

`kubectl` surfaces the deprecation on every use:

```console
$ kubectl apply -f webapp-v1alpha1.yaml
Warning: platform.miportfolio.com/v1alpha1 WebApp is deprecated; use platform.miportfolio.com/v1
webapp.platform.miportfolio.com/app created
```

## How conversion works

Conversion uses the **hub-and-spoke** model. `v1` is the hub and implements a
marker method; every other version is a spoke that converts to and from it:

```go
func (*WebApp) Hub() {}                                    // api/v1
func (src *WebApp) ConvertTo(dst conversion.Hub) error     // api/v1alpha1
func (dst *WebApp) ConvertFrom(src conversion.Hub) error   // api/v1alpha1
```

With N served versions this is N−1 conversions instead of N×(N−1) pairwise ones,
and the compatibility code lives in the old versions rather than accumulating in
the current one.

The API server calls the operator's `/convert` endpoint whenever a client asks
for a version other than the stored one — on reads, on writes, and during
storage migration.

### Conversion cannot fail on data the old schema accepted

This is the constraint that shapes the implementation. `v1alpha1` allowed
`maxReplicas` on its own, and never enforced `maxReplicas >= minReplicas`. Such
objects exist in etcd. If conversion rejected them, they would become
**unreadable** — not just un-updatable — and `kubectl get` would error for every
client, on a version they may not even be using.

So conversion completes rather than rejects:

| Stored under `v1alpha1` | Converted to `v1` |
|---|---|
| only `maxReplicas: 5` | `minReplicas: 1`, `maxReplicas: 5`, `cpuThresholdPercent: 80` |
| `minReplicas: 8`, `maxReplicas: 3` | `maxReplicas` widened to 8 to satisfy the CEL rule |
| none of the three | `autoscaling` left unset — autoscaling off |

Validation is the job of admission, on the way in. Conversion's job is to make
existing data readable.

### Fields that only exist in v1

`resources`, `readinessPath`, `security` and `podDisruptionBudget` were added
after `v1alpha1` was frozen. Reading a `WebApp` through `v1alpha1` simply cannot
show them — the stored object keeps them, the old view has nowhere to put them.
That lossiness is the price of serving an old version, and the reason clients
should migrate.

Note what this means in practice: a client that reads through `v1alpha1` and
writes the object back **will drop those fields**. Round-tripping through an old
version is not safe for objects using newer features, which is exactly why the
old version is deprecated rather than kept indefinitely.

## The webhook-free install serves only v1

Version conversion *is* a webhook, so an install without webhooks cannot do it.
Serving `v1alpha1` there with conversion strategy `None` would be worse than not
serving it: the API server would return the stored `v1` object relabelled as
`v1alpha1`, without remapping the autoscaling fields, and a client would silently
read "no autoscaling configured".

`dist/install.yaml` therefore marks `v1alpha1` as not served. A request for it
gets a clear error instead of wrong data. `dist/install-with-webhooks.yaml`
serves both.

## Testing

Round-trip conversion is covered by unit tests in
[`api/v1alpha1/webapp_conversion_test.go`](../api/v1alpha1/webapp_conversion_test.go):
an object written as `v1alpha1`, converted to the hub and read back comes out
unchanged, and each of the completion rules above is asserted explicitly.

```bash
go test ./api/...
```

## Migrating from v1alpha1

1. Change `apiVersion` to `platform.miportfolio.com/v1`.
2. Move `minReplicas`, `maxReplicas` and `cpuThreshold` under `autoscaling`,
   renaming the last one to `cpuThresholdPercent`.
3. Make sure all three are present, and that `maxReplicas >= minReplicas` — `v1`
   enforces both.

Existing objects need no action: they are already stored as `v1`.
