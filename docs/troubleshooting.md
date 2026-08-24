# Troubleshooting

Organised by symptom. Every entry below is a failure that has actually been hit
while developing or operating this project.

## First moves

```bash
# What does the operator think the state is?
kubectl describe webapp <name>

# What did it do, and what went wrong?
kubectl logs -n webapp-operator-system deployment/webapp-operator-controller-manager --tail=100

# Is the operator running at all?
kubectl get pods -n webapp-operator-system
```

The `Degraded` condition carries the reconcile error, and the `Events` section
of `describe` carries the history. Start there before reading logs.

---

## The WebApp is rejected on `kubectl apply`

### `spec.image … uses the mutable "latest" tag`

Working as intended — see [Admission webhooks](webhooks.md). Pin a version
(`my-api:1.4.0`) or a digest (`my-api@sha256:…`).

### `spec.autoscaling: Invalid value: maxReplicas must be greater than or equal to minReplicas`

A CEL rule on the object. Fix the pair; the API server will not store an
inconsistent autoscaling block.

### `spec.port: Invalid value: … should be less than or equal to 65535`

Schema validation. Ports are `1`–`65535`.

### `Internal error occurred: failed calling webhook … connection refused`

The webhook is registered but the manager is not reachable. Both webhooks use
`failurePolicy: fail`, so the API server rejects rather than admitting
unchecked. Check the manager is running and its certificate is present:

```bash
kubectl get pods -n webapp-operator-system
kubectl get certificate -n webapp-operator-system
kubectl get validatingwebhookconfiguration
```

The usual cause is cert-manager missing or not ready. If you do not want
webhooks at all, install with `webhook.enable=false`.

---

## Pods never become ready

### `ImagePullBackOff`

```bash
kubectl describe pod -l app=<webapp-name> | grep -A5 Events
```

The image does not exist, or the cluster cannot authenticate to the registry.
Note that a `kind` cluster does not have your local Docker images — load them
explicitly:

```bash
kind load docker-image my-api:1.4.0 --name <cluster>
```

### `CreateContainerConfigError` with `container has runAsNonRoot and image will run as root`

`spec.security.runAsNonRoot: true` was set for an image that has no non-root
`USER`. Either use an image built to run unprivileged (for example
`nginxinc/nginx-unprivileged` instead of `nginx`), set `spec.security.runAsUser`
to a valid UID, or drop the setting.

### The container starts, then exits — "permission denied" writing a file

`spec.security.readOnlyRootFilesystem: true` on an image that writes to its own
filesystem. This is image-dependent, which is exactly why it is opt-in. Remove
it, or mount writable volumes for the paths the image needs.

### The container cannot bind its port

The operator drops **all** Linux capabilities from managed containers, and
binding a port below 1024 requires `CAP_NET_BIND_SERVICE`. Use a port such as
8080 and an image that listens on it. This is a deliberate trade-off; see
[Design notes](design.md#hardening-defaults-are-the-subset-that-is-safe-for-any-image).

### Pods are `Running` but `AVAILABLE` stays `False`

`Available` is computed from pods that pass their **readiness probe**, not from
pods that started. If `spec.readinessPath` points at a path the application does
not serve, the probe fails forever. Check:

```bash
kubectl describe pod -l app=<webapp-name> | grep -A3 Readiness
```

Remove `readinessPath` to fall back to a TCP check on `spec.port`.

---

## The HPA shows `<unknown>` for CPU

```console
$ kubectl get hpa
NAME                REFERENCE                      TARGETS              MINPODS   MAXPODS
my-api-autoscaler   Deployment/my-api-deployment   cpu: <unknown>/70%   2         10
```

Two independent causes, and both must be resolved:

1. **No CPU request on the container.** Utilization is a percentage of the
   request, so without one there is nothing to compute. The defaulting webhook
   injects `100m` — but only if the webhooks are installed. Without them, set it
   yourself:

   ```yaml
   spec:
     resources:
       requests:
         cpu: 100m
   ```

2. **No metrics source in the cluster.** Install metrics-server:

   ```bash
   kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
   # kind uses self-signed kubelet certificates
   kubectl patch deployment metrics-server -n kube-system --type=json \
     -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
   ```

Verify with `kubectl top pods` before blaming the operator.

---

## The Deployment is rewritten in a loop

Symptoms: `metadata.generation` on the Deployment climbs continuously, several
ReplicaSets exist at once, pods churn, and `webapp_child_operations_total` with
`operation="updated"` rises without pause.

```bash
# generation should be stable in a steady state
for i in 1 2 3; do kubectl get deployment <name>-deployment -o jsonpath='{.metadata.generation}{"\n"}'; sleep 3; done
```

Almost always **two controllers fighting over the same objects**: a local
`make run` alongside the operator deployed in the cluster, or two clusters'
contexts crossed. Leader election does *not* prevent this — the two processes
hold separate leases. Stop one of them:

```bash
kubectl scale deployment -n webapp-operator-system webapp-operator-controller-manager --replicas=0
# ... or stop the local `make run`
```

The same symptom appears when the two are running **different versions** of the
operator, since each writes a pod template the other considers wrong.

---

## Deleting a WebApp leaves resources behind

It should not: children carry owner references and are garbage-collected. If
they persist, check that the owner reference survived:

```bash
kubectl get deployment <name>-deployment -o jsonpath='{.metadata.ownerReferences}'
```

A resource created by hand with the same name is **not** adopted and will not be
cleaned up. Note also that removing `spec.autoscaling` deletes the HPA on the
next reconcile — that is intended, not a leak.

---

## `kind create cluster` fails

```
ERROR: failed to create cluster: could not find a log line that matches
"Reached target .*Multi-User System.*|detected cgroup v1"
```

Typically resource exhaustion rather than a Kubernetes problem — frequently
`inotify` limits on WSL2 when other clusters are already running:

```bash
cat /proc/sys/fs/inotify/max_user_instances   # 128 is the low default
kind get clusters                             # how many are already up?
```

Delete a cluster you are not using, or raise the limits (needs root):

```bash
sudo sysctl fs.inotify.max_user_instances=512
sudo sysctl fs.inotify.max_user_watches=524288
```

---

## Getting help

Include the following when opening an issue:

```bash
kubectl version --short
kubectl get webapp <name> -o yaml
kubectl describe webapp <name>
kubectl logs -n webapp-operator-system deployment/webapp-operator-controller-manager --tail=200
```
