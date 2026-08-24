# Prior art, and when not to use this

A `WebApp` resource is not the only way to stop hand-writing three manifests per
service. Below is an honest comparison with the obvious alternatives, including
the cases where this operator is the wrong answer.

## The alternatives

### A plain Helm chart

Define Deployment, Service and HPA once as templates, and let every team supply
a `values.yaml`.

**Where it wins.** No custom code to maintain, no controller to run, no CRD to
version. Everyone already knows it. Rendering is fully inspectable before
anything reaches the cluster (`helm template`), and `helm diff` shows the change.

**Where it loses.** A chart is applied, not *maintained*. Nothing notices when
someone edits the generated Deployment by hand: the drift stays until the next
`helm upgrade` — which may be weeks away, and which will then quietly revert it
with no record. There is nowhere to put a `status`, so "is my app healthy?" is
answered by inspecting the children, not by reading the object you created. And
policy can only be enforced at template-render time, which a user can bypass by
applying a Deployment directly.

**Verdict: for most teams, a chart is enough.** If nobody is editing resources
out of band and nobody needs a status to key automation off, an operator is
strictly more moving parts for the same result.

### [kro](https://kro.run/) (Kube Resource Orchestrator)

Declare a `ResourceGraphDefinition` and kro generates a CRD and reconciles the
graph — a custom API without writing a controller.

**Where it wins.** It removes exactly the part this repository spends most of
its code on. Abstractions become configuration, so a platform team can ship a
new one in an afternoon, and every one of them gets the same reconciliation
behaviour for free.

**Where it loses.** Logic that is not "render this graph" is hard to express:
the conditional CPU request injected here for autoscaling, an admission policy
that parses image references, custom conditions computed from operand state. You
also take on kro as a dependency in the control plane.

**Verdict: if the abstraction is a static graph of resources, kro is very likely
the better tool.** This operator earns its keep only because of the behaviour
around the graph, not the graph itself.

### [Knative Serving](https://knative.dev/)

`kind: Service` with an image and a port, and you get deployment, networking,
revisions, autoscaling and scale-to-zero.

**Where it wins.** Far more capable than this: request-driven autoscaling,
scale-to-zero, traffic splitting between revisions, built-in rollbacks. It is a
mature project with real production usage.

**Where it loses.** It brings a networking layer and a substantial control plane,
and it is opinionated about how traffic reaches your app. Its abstraction is
*serverless-shaped* — excellent for stateless request handlers, awkward for
anything expected to be always-on with a fixed replica floor.

**Verdict: if the workloads are HTTP services and the cluster can host it,
Knative offers more than this operator ever will.**

## When this operator is the wrong answer

- **The workload does not fit the shape.** `WebApp` assumes one container, one
  port, HTTP-ish, stateless. Anything with sidecars, several ports, volumes or
  StatefulSet semantics is being forced through an abstraction that does not fit,
  and the escape hatch will end up being a raw Deployment anyway.
- **You need every Deployment knob.** Node affinity, topology spread, init
  containers, arbitrary env vars, secrets — none of it is exposed. Adding all of
  it would reinvent the Deployment API with extra steps and no benefit.
- **One team, a handful of services.** The abstraction pays off when many teams
  ship many similar services and consistency has real value. Below that, it is a
  CRD, a controller and an upgrade path to maintain in exchange for saving a few
  lines of YAML.
- **Nobody will own it.** An operator is a control-plane component: it needs
  upgrades, monitoring and someone on call for it. An unowned operator is worse
  than the manifests it replaced.

## When it does earn its place

- Many teams deploying the same *shape* of service, where drift and
  inconsistency between them is a real cost.
- Guardrails that must hold regardless of how a resource is submitted —
  admission runs whether the change came from CI, from `kubectl`, or from a
  script nobody remembers writing.
- Defaults that must be *correct*, not merely suggested: the CPU request that
  makes autoscaling function at all is applied whether or not anyone remembered
  it.
- A meaningful `status`: `kubectl get webapp` answering the health question
  directly, and automation keying off `Available` and `observedGeneration`
  instead of inspecting three child objects.

The honest summary: **this is a platform primitive, not a deployment tool.** Its
value is the guardrails and the feedback loop around the resources, not the
resources themselves. If those are not worth a control-plane component to you,
use a Helm chart.
