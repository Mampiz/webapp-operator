#!/usr/bin/env bash
# End-to-end demo of the WebApp operator on a local kind cluster.
#
# Walks through: install -> create a WebApp -> self-healing -> admission control.
# Written to be recorded with asciinema:
#   asciinema rec demo.cast -c "make demo"
set -euo pipefail

CLUSTER="${CLUSTER:-webapp-demo}"
IMG="${IMG:-controller:demo}"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.16.2}"

step() { printf '\n\033[1;36m▶ %s\033[0m\n' "$1"; }
run()  { printf '\033[0;90m$ %s\033[0m\n' "$*"; "$@"; }
pause() { sleep "${PAUSE:-2}"; }

step "1/7  Create a local kind cluster"
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  echo "cluster '$CLUSTER' already exists, reusing it"
else
  run kind create cluster --name "$CLUSTER"
fi
kubectl config use-context "kind-${CLUSTER}" >/dev/null
pause

step "2/7  Install cert-manager (issues the TLS certs the webhooks need)"
run kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
run kubectl wait --for=condition=Available --timeout=300s -n cert-manager deployment/cert-manager-webhook
pause

step "3/7  Build the operator image and load it into the cluster"
run make docker-build IMG="$IMG"
run kind load docker-image "$IMG" --name "$CLUSTER"
pause

step "4/7  Deploy the operator (CRD + RBAC + webhooks)"
run make deploy IMG="$IMG"
run kubectl wait --for=condition=Available --timeout=300s \
  -n webapp-operator-system deployment/webapp-operator-controller-manager
pause

step "5/7  Create a WebApp — one resource in, a full stack out"
run kubectl apply -f config/samples/platform_v1_webapp.yaml
run kubectl wait --for=condition=Available --timeout=180s deployment/webapp-sample-deployment
run kubectl get webapp
run kubectl get deployment,service,hpa,pdb
pause

step "6/7  Self-healing — delete the Deployment and watch it come back"
run kubectl delete deployment webapp-sample-deployment
sleep 3
run kubectl get deployment
pause

step "7/7  Admission control — a mutable tag is rejected before it is stored"
echo "\$ kubectl apply -f config/samples/platform_v1_webapp_invalid.yaml"
if kubectl apply -f config/samples/platform_v1_webapp_invalid.yaml 2>&1; then
  echo "UNEXPECTED: the invalid WebApp was accepted" >&2
  exit 1
fi

printf '\n\033[1;32m✔ Demo complete.\033[0m  Tear down with:  kind delete cluster --name %s\n' "$CLUSTER"
