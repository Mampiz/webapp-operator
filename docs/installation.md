# Installation

## Requirements

| Component | Version |
|---|---|
| Kubernetes | 1.29 – 1.36 |
| cert-manager | ≥ 1.14, **only** if admission webhooks are enabled |

The operator needs cluster-scoped permissions: it creates and manages
Deployments, Services, HorizontalPodAutoscalers and PodDisruptionBudgets in the
namespaces where `WebApp` resources are created.

> Install a **tagged release**. The manifests on `main` track unreleased work and
> may reference an image that has not been published.

## Option A — single manifest

```bash
kubectl apply -f https://github.com/Mampiz/webapp-operator/releases/download/v1.0.0/install.yaml
```

This bundles the CRD, RBAC, the `webapp-operator-system` namespace and the
controller Deployment.

Verify:

```console
$ kubectl wait --for=condition=Available --timeout=300s \
    -n webapp-operator-system deployment/webapp-operator-controller-manager
deployment.apps/webapp-operator-controller-manager condition met

$ kubectl get crd webapps.platform.miportfolio.com
NAME                               CREATED AT
webapps.platform.miportfolio.com   2026-08-24T10:12:31Z
```

## Option B — Helm

```bash
helm install webapp-operator ./dist/chart \
  --namespace webapp-operator-system --create-namespace
```

The chart installs with **webhooks disabled**, so it works on a cluster without
cert-manager. Useful values:

| Value | Default | Meaning |
|---|---|---|
| `manager.image.repository` | `ghcr.io/mampiz/webapp-operator` | Operator image |
| `manager.image.tag` | `1.0.0` | Image tag |
| `manager.replicas` | `1` | Controller replicas; leader election picks one active |
| `webhook.enable` | `false` | Enable admission webhooks (needs cert-manager) |
| `certManager.enabled` | `false` | Render cert-manager `Certificate` resources |
| `prometheus.enable` | `false` | Render the `ServiceMonitor` |

## Enabling the admission webhooks

Webhooks serve over TLS, so the API server needs a certificate it trusts. That
is what cert-manager provides.

```bash
# 1. cert-manager
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.16.2/cert-manager.yaml
kubectl wait --for=condition=Available --timeout=300s \
  -n cert-manager deployment/cert-manager-webhook

# 2. the operator, with webhooks on
helm upgrade --install webapp-operator ./dist/chart \
  --namespace webapp-operator-system --create-namespace \
  --set webhook.enable=true --set certManager.enabled=true
```

With the plain manifest, `dist/install.yaml` already contains the webhook
configuration and the cert-manager `Certificate`; installing cert-manager first
is enough.

Confirm the policy is active:

```console
$ kubectl apply -f config/samples/platform_v1_webapp_invalid.yaml
Error from server (Forbidden): admission webhook "vwebapp-v1.kb.io" denied the request:
spec.image "nginx:latest" uses the mutable "latest" tag; pin an explicit version for reproducibility
```

See [Admission webhooks](webhooks.md) for what the webhooks enforce and why.

## Running more than one replica

The manager runs with leader election enabled, so extra replicas are warm
standbys rather than parallel workers. Raising `manager.replicas` shortens
recovery if the active pod dies; it does not increase throughput.

> Never run `make run` against a cluster that already has the operator
> deployed. The two processes hold separate leases and will fight over the same
> child resources, rewriting the Deployment continuously. See
> [Troubleshooting](troubleshooting.md#the-deployment-is-rewritten-in-a-loop).

## Upgrading

`WebApp` has a single stored API version, so upgrades are a rolling image change:

```bash
helm upgrade webapp-operator ./dist/chart --set manager.image.tag=1.1.0
# or
kubectl apply -f https://github.com/Mampiz/webapp-operator/releases/download/v1.1.0/install.yaml
```

Apply the new CRD before the new controller when a release adds fields; the
bundled manifest already orders them correctly. Existing `WebApp` objects are
untouched by an upgrade, and the first reconcile after it converges any new
defaults. Check [CHANGELOG.md](../CHANGELOG.md) for behavioural changes.

## Uninstalling

```bash
kubectl delete webapp --all --all-namespaces   # first: removes managed workloads
helm uninstall webapp-operator -n webapp-operator-system
kubectl delete crd webapps.platform.miportfolio.com
```

Delete the `WebApp` objects **before** the CRD. Removing the CRD first deletes
the custom resources without the operator being able to observe it; the
generated Deployments and Services are then garbage-collected by Kubernetes
through their owner references, but nothing reports on the process.
