# Getting started

Deploy the operator on a local cluster and manage your first application with it.
Expect this to take about ten minutes, most of which is pulling images.

## Prerequisites

| Tool | Purpose |
|---|---|
| [Docker](https://docs.docker.com/engine/install/) | Runs the local cluster |
| [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) | Creates the Kubernetes cluster |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | Talks to the cluster |

## The guided tour

Everything below is scripted. From a clone of the repository:

```bash
make demo
```

This creates a kind cluster, installs cert-manager and the operator, creates a
`WebApp`, deletes its Deployment to show self-healing, and finishes by having the
admission webhook reject a mutable image tag.

![The operator in action](assets/demo.gif)

Tear the cluster down when you are finished:

```bash
kind delete cluster --name webapp-demo
```

## Doing it by hand

### 1. Create a cluster and install the operator

```bash
kind create cluster --name webapp-dev
make install deploy IMG=ghcr.io/mampiz/webapp-operator:1.0.0
kubectl wait --for=condition=Available --timeout=300s \
  -n webapp-operator-system deployment/webapp-operator-controller-manager
```

`make install` registers the `WebApp` custom resource definition; `make deploy`
runs the operator itself. See [Installation](installation.md) for the
webhook-enabled and Helm variants.

### 2. Create a WebApp

```yaml
# my-api.yaml
apiVersion: platform.miportfolio.com/v1
kind: WebApp
metadata:
  name: my-api
spec:
  image: nginxinc/nginx-unprivileged:1.27-alpine
  port: 8080
  replicas: 2
```

```bash
kubectl apply -f my-api.yaml
```

Two constraints are worth knowing before you swap in your own image:

- The tag must be explicit. `latest`, or no tag at all, is rejected — see
  [Admission webhooks](webhooks.md).
- Containers run without any Linux capabilities, so they cannot bind a port
  below 1024. Use 8080 rather than 80.

### 3. Inspect what the operator built

```console
$ kubectl get webapp
NAME     IMAGE                                     READY   AVAILABLE   AGE
my-api   nginxinc/nginx-unprivileged:1.27-alpine   2       True        30s

$ kubectl get deployment,service
NAME                        READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/my-api-deployment   2/2     2            2       30s

NAME                     TYPE        CLUSTER-IP     PORT(S)    AGE
service/my-api-service   ClusterIP   10.96.59.187   8080/TCP   30s
```

`AVAILABLE` comes from the operator's own `Available` condition, which is
computed from pods that pass their readiness probe — not merely from pods that
started.

### 4. Reach the application

```bash
kubectl port-forward service/my-api-service 8080:8080
curl -s localhost:8080 | head -5
```

### 5. Watch it heal

Delete a resource the operator owns and it is rebuilt on the next reconcile:

```console
$ kubectl delete deployment my-api-deployment
deployment.apps "my-api-deployment" deleted

$ kubectl get deployment
NAME                READY   UP-TO-DATE   AVAILABLE   AGE
my-api-deployment   2/2     2            2           2s
```

### 6. Turn on autoscaling

```bash
kubectl patch webapp my-api --type=merge -p '{
  "spec": {"autoscaling": {"minReplicas": 2, "maxReplicas": 10, "cpuThresholdPercent": 70}}
}'
```

An HPA appears, and from now on it owns the replica count — `spec.replicas` is
ignored while autoscaling is enabled.

```console
$ kubectl get hpa
NAME                REFERENCE                      TARGETS    MINPODS   MAXPODS   REPLICAS
my-api-autoscaler   Deployment/my-api-deployment   cpu: 0%/70%      2        10          2
```

If `TARGETS` shows `<unknown>`, the cluster has no metrics-server. Install one:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
# kind uses self-signed kubelet certificates
kubectl patch deployment metrics-server -n kube-system --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
```

### 7. Clean up

Deleting the `WebApp` removes everything it created, through owner references:

```bash
kubectl delete webapp my-api
```

## Next steps

- [API reference](api-reference.md) — every field you can set.
- [Observability](observability.md) — metrics, dashboard and alerts.
- [Troubleshooting](troubleshooting.md) — when something does not come up.
