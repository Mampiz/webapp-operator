# Design notes

Why this operator is built the way it is. These are the decisions a reviewer is
most likely to question, with the reasoning and the trade-offs written down.

## The reconcile loop is level-based, not edge-triggered

The controller never asks "what event happened?". Every invocation reads the
current desired state (the `WebApp` spec) and the current actual state (the
child objects), and corrects the difference.

This matters because the work queue does not deliver events, it delivers **keys**
(`namespace/name`). Several rapid changes collapse into a single queued key, and
a periodic resync enqueues objects with no change at all. A controller that
reacted to "a WebApp was created" would miss the coalesced updates and would
misbehave on resyncs. Reacting to state instead makes the loop naturally
**idempotent**: running it once or a thousand times converges to the same result.

## `CreateOrUpdate` instead of `Create`

Each child is reconciled with `controllerutil.CreateOrUpdate`: fetch, apply the
desired state through a mutate function, and write only if something changed.
A bare `Create` would fail with `AlreadyExists` on the second reconcile and put
the object into a permanent error/retry cycle.

The return value (`created`, `updated`, `unchanged`) is also what drives events
and metrics, so a steady-state reconcile emits nothing — no event spam, no fake
"success" counts on the child-operation metric.

## What happens if the operator dies mid-reconcile

Nothing is lost, and nothing needs cleaning up. Each child write is an
independent API call, so a crash between the Deployment and the Service leaves
the Deployment created and the Service missing — which is simply a state the next
reconcile corrects. There is no partial-transaction problem to unwind because
the loop never assumes it starts from a clean slate.

This is also why there are **no finalizers**: the operator holds no external
state. Cleanup is delegated to Kubernetes garbage collection via owner
references, which keeps deletion correct even if the operator is down at the
moment the `WebApp` is deleted.

## Multiple replicas and leader election

The manager runs with leader election enabled, so exactly one replica reconciles
at a time; the others stand by and take over if the leader's lease expires. Two
active reconcilers writing the same children would fight each other, each
undoing the other's write, producing a hot loop of Deployment revisions.

That failure mode is not theoretical: it is exactly what happens when a
locally-run operator (`make run`) is left running against a cluster that already
has the operator deployed. The two are separate processes with separate leases,
so leader election does not protect against it — stop one of them.

## The HPA owns `spec.replicas` when autoscaling is enabled

When `spec.autoscaling` is set, the mutate function deliberately does **not**
write `deploy.Spec.Replicas`. The HPA is a second controller writing that same
field; if the operator also asserted a value on every reconcile, the two would
overwrite each other continuously.

The rule is: **one writer per field**. `spec.replicas` therefore only applies when
autoscaling is absent, and the API documents it as ignored otherwise.

The corollary is that CPU-based autoscaling needs a CPU **request** — target
utilization is a percentage of the request, so without one the HPA reports
`<unknown>` and never scales. Rather than let users create a silently broken
autoscaler, the defaulting webhook injects a request when autoscaling is enabled
and none was given.

## Where defaults live

There are three places a default can be applied, and choosing among them is a
correctness decision, not a style one.

| Default | Applied by | Why there |
|---|---|---|
| `spec.replicas` → 1 | **CRD schema** (`+kubebuilder:default=1`) | The API server applies it. Always present, no components involved. |
| CPU request when autoscaling is on | **Webhook** *and* **reconciler** | Conditional on another field, so it cannot be a schema default. |
| `app.kubernetes.io/managed-by` label | **Webhook** only | Convenience, not correctness. |
| Image tag policy | **Webhook** only | Policy, and inherently uncheckable by a static schema. |

The rule behind that table: **anything correctness depends on must not live only
in an optional component.**

Admission webhooks are optional here — they need TLS certificates from
cert-manager, so both the Helm chart and the plain `install.yaml` ship without
them. If the autoscaling CPU request existed only in the defaulting webhook, then
a default `helm install` plus a `WebApp` with autoscaling would silently
reproduce the exact bug the webhook was written to prevent: an HPA reporting
`<unknown>` forever, never scaling.

So the request is applied twice, deliberately:

- the **webhook** writes it onto the stored object, which makes the resource
  self-describing — `kubectl get webapp -o yaml` shows what will actually run;
- the **reconciler** applies the same value when building the Deployment,
  because a controller must never assume admission ran.

They agree on one constant, and a test reconciles a `WebApp` with autoscaling and
no resources at all — with no webhook in the path — asserting the Deployment
still carries the request.

Policy defaults are the opposite case and are left webhook-only on purpose.
Losing the image-tag rule when webhooks are absent degrades the guarantees the
platform offers, but it cannot corrupt a workload. That trade-off is stated
explicitly in the installation guide rather than hidden.

## Removing an optional block deletes its object

Owner references only garbage-collect when the *parent* is deleted. Removing
`spec.autoscaling` from a live `WebApp` leaves the parent alive, so the HPA would
survive forever as an orphan that keeps scaling the Deployment. The reconcile
explicitly deletes the HPA (and the PodDisruptionBudget) when its block is
absent, treating "not requested" as a desired state to converge to, not as
"leave alone".

## Hardening defaults are the subset that is safe for any image

Every pod gets `capabilities: drop: [ALL]`, `allowPrivilegeEscalation: false`
and the `RuntimeDefault` seccomp profile. These cannot be disabled through the
API, because they are safe for essentially any workload.

`runAsNonRoot` and `readOnlyRootFilesystem` are **opt-in** instead. They depend
on how the image was built: enabling them by default would break every image
that starts as root or writes to its own filesystem, which would make the
abstraction unusable and push people back to raw Deployments.

Note the consequence of dropping all capabilities: a container can no longer
bind a privileged port, because that requires `CAP_NET_BIND_SERVICE`. The sample
therefore uses an unprivileged image listening on 8080.

## Readiness defaults to TCP, and there is no liveness probe

Without a readiness probe, a pod counts as ready the moment its process starts,
so `status.readyReplicas` — and the `Available` condition derived from it — would
claim the app is serving before it can answer a request. A TCP connection to
`spec.port` is the strongest check that can be made without assuming anything
about the application, and `spec.readinessPath` upgrades it to an HTTP GET.

No liveness probe is generated. A readiness probe that fails removes a pod from
service; a liveness probe that fails **restarts** it, so a badly chosen default
turns a slow dependency into a cluster-wide restart loop. Restarting is a
decision that needs application knowledge, so the operator does not guess.

## Status is patched, not updated, and only when it changed

`Status().Update()` sends the whole object and fails on conflict if anything
else touched it. The reconcile uses `Status().Patch()` with a merge patch
computed against the object as it was read, so concurrent writers to unrelated
fields do not cause lost updates.

The patch is also skipped entirely when the computed status is deep-equal to the
observed one. Without that check every resync would write the object, bumping
its `resourceVersion`, waking the watch, and enqueuing another reconcile — a
self-sustaining loop that does no useful work.

## Conditions carry `observedGeneration`

Each condition records the `metadata.generation` it was computed from. A client
comparing `status.observedGeneration` with `metadata.generation` can tell
"`Available=True` for the spec I just submitted" apart from "`Available=True`,
but for the previous spec, and the new one has not been processed yet".
