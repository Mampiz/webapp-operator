# Scale

Measured behaviour as the number of `WebApp` objects grows. These are numbers
from an actual run, not estimates — reproduce them with `make scale-test`.

## Results

| WebApps | Reconcile p50 | Reconcile p95 | Peak queue depth | Manager CPU (cumulative) | Manager RSS |
|--:|--:|--:|--:|--:|--:|
| 100 | 25 ms | 50 ms | 1 | 3.7 s | 34 MB |
| 250 | 25 ms | 50 ms | 1 | 6.8 s | 41 MB |

**Reconcile latency does not move between 100 and 250 WebApps.** Each
reconciliation is a fixed amount of work — read one object, compare four
children — so per-object cost is independent of how many objects exist. The
p50/p95 figures come from the histogram buckets, so 25 ms and 50 ms are bucket
boundaries rather than exact values; the useful reading is that both stayed in
the same bucket as the population grew 2.5×.

**The queue never backed up.** A peak depth of 1 means the controller drained
work as fast as it arrived, with a single worker. Sustained depth is what the
`WebAppOperatorWorkQueueBacklog` alert watches for; nothing in this range comes
close.

**Memory grew by 7 MB for 150 additional WebApps**, roughly 45 KB each. That is
the informer cache holding the objects and their children, and it is linear —
the cost to expect is the size of the objects being watched, not a per-object
overhead in the controller.

**CPU is the figure that scales with the population**, as it should: 3.7 s of
cumulative CPU to reconcile the first 100, 6.8 s by 250. Roughly 30 ms of CPU
per WebApp brought to steady state, spread over the reconciles each object needs
before it settles.

## What was not measured

- **Above 250 objects.** The test machine (7 GB of RAM, a single-node kind
  cluster) becomes the bottleneck before the controller does — 250 WebApps is
  already 250 Deployments and 500 pods' worth of scheduling. The numbers above
  describe the controller, and past this point they would describe the laptop.
- **Admission latency.** The webhooks are deliberately taken out of the path
  (see the methodology below), so these figures cover the reconciler only.
- **Multiple concurrent workers.** The manager runs the default single worker
  per controller. Raising `MaxConcurrentReconciles` was not needed at this scale
  and was not exercised.

## Methodology

```bash
make scale-test SIZES="100 250"
# or: ./hack/scale-test.sh 100 250
```

The script:

1. Stands down the in-cluster manager and starts one locally with a plaintext
   metrics endpoint, so exactly one reconciler is running. Two would contend for
   the same objects and the numbers would describe the fight rather than the
   operator.
2. Creates `WebApp` objects in batches and waits for the work queue to drain,
   which is the point at which every object has been reconciled at least once.
3. Reads the results from the manager's own `/metrics`:
   `controller_runtime_reconcile_time_seconds_bucket` for the quantiles,
   `workqueue_depth` for the backlog, `process_cpu_seconds_total` and
   `process_resident_memory_bytes` for the manager itself.
4. Restores everything it changed on exit.

### One thing the benchmark had to work around

The admission webhooks are registered with `failurePolicy: fail`. Standing down
the manager makes them unreachable, so the API server **rejects every `WebApp`
write** — correct, deliberate behaviour, and fatal to a benchmark that works by
creating hundreds of them. The script therefore parks the webhook configurations
for the duration and restores them afterwards.

That is also why the first version of this page could not be written: the run
reported "100 of the applies failed", so the latency figures it printed were
measuring an idle controller. The script now counts failed applies and says so,
because a benchmark that silently measures nothing is worse than no benchmark.

## Environment

| | |
|---|---|
| Cluster | kind, single node, Kubernetes v1.36.1 |
| Host | WSL2 on Linux, 16 CPUs, 7 GB RAM |
| Operator | run locally via `go run`, one worker, leader election off |
| Operand image | `nginxinc/nginx-unprivileged:1.27-alpine`, 1 replica each |

Absolute numbers on a laptop are not a capacity plan for a production cluster.
What travels is the *shape*: flat per-object latency, a queue that keeps up, and
memory linear in the number of watched objects.
