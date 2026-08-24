# Scale

Measured behaviour as the number of `WebApp` objects grows. These are numbers
from an actual run, not estimates — reproduce them with `make scale-test`.

## Results

| WebApps | Reconcile p50 | Reconcile p95 | Peak queue depth | Manager CPU (cumulative) | Manager RSS |
|--:|--:|--:|--:|--:|--:|
| 100 | 25 ms | 50 ms | 2 | 2.6 s | 53 MB |
| 250 | 25 ms | 50 ms | 1 | 5.8 s | 55 MB |

**Reconcile latency does not move between 100 and 250 WebApps.** Each
reconciliation is a fixed amount of work — read one object, compare four
children — so the per-object cost does not depend on how many objects exist. The
quantiles come from histogram buckets, so 25 ms and 50 ms are bucket boundaries
rather than exact values; the useful reading is that both stayed in the same
bucket while the population grew 2.5×.

**The queue never backed up.** A peak depth of 2 at 100 objects and 1 at 250
means the controller drained work as fast as it arrived, with a single worker.
Sustained depth is what the `WebAppOperatorWorkQueueBacklog` alert watches for;
nothing in this range approaches it.

**CPU scales with the population, as it should**: 2.6 s of cumulative CPU to
bring the first 100 to steady state, 5.8 s by 250 — roughly 25 ms of CPU per
WebApp, spread over the reconciles each object needs before it settles.

**Memory stayed essentially flat**, 53 MB to 55 MB. Resident memory in a Go
process is a coarse signal — the runtime returns pages to the OS on its own
schedule — so the honest conclusion is only that 150 additional WebApps did not
move it measurably at this scale, not that the informer cache is free.

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

A second run had to be discarded as well. The manager was being started with
`go run`, which compiles the binary and runs it as a *child* process — so
stopping `go run` on cleanup left that child alive, still holding the metrics
port. The next run's scrapes went to the stale process, and the figures
described a previous experiment. The script now builds the binary and runs it
directly, and refuses to start if anything is already listening on the metrics
port.

## Environment

| | |
|---|---|
| Cluster | kind, single node, Kubernetes v1.36.1 |
| Host | WSL2 on Linux, 16 CPUs, 7 GB RAM |
| Operator | run locally via `go run`, one worker, leader election off |
| Operand image | `nginxinc/nginx-unprivileged:1.27-alpine`, 1 replica each |

Absolute numbers on a laptop are not a capacity plan for a production cluster.
What travels is the *shape*: flat per-object latency, a queue that keeps up, and
CPU that grows with the number of objects while per-object cost stays constant.
