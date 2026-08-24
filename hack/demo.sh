#!/usr/bin/env bash
# End-to-end demo of the WebApp operator on a local kind cluster.
#
# Walks through: install -> create a WebApp -> self-healing -> admission control.
# Written to be recorded with asciinema:
#   asciinema rec demo.cast -c "make demo"
set -euo pipefail

CLUSTER="${CLUSTER:-webapp-demo}"
# DEMO_SKIP_SETUP=1 assumes the cluster already has the operator installed and
# jumps straight to the usage steps. Used when recording the documentation GIF,
# so the recording is not dominated by an image build and a cert-manager install.
SKIP_SETUP="${DEMO_SKIP_SETUP:-0}"
IMG="${IMG:-controller:demo}"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.16.2}"

step() { printf '\n\033[1;36m▶ %s\033[0m\n' "$1"; }
run()  { printf '\033[0;90m$ %s\033[0m\n' "$*"; "$@"; }
pause() { sleep "${PAUSE:-2}"; }

if [[ "$SKIP_SETUP" != "1" ]]; then

step "1/8  Create a local kind cluster"
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  echo "cluster '$CLUSTER' already exists, reusing it"
else
  run kind create cluster --name "$CLUSTER"
fi
kubectl config use-context "kind-${CLUSTER}" >/dev/null
pause

step "2/8  Install cert-manager (issues the TLS certs the webhooks need)"
run kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
run kubectl wait --for=condition=Available --timeout=300s -n cert-manager deployment/cert-manager-webhook
pause

step "3/8  Build the operator image and load it into the cluster"
run make docker-build IMG="$IMG"
run kind load docker-image "$IMG" --name "$CLUSTER"
pause

step "4/8  Install metrics-server (the HPA needs a metrics source)"
if kubectl get deployment metrics-server -n kube-system >/dev/null 2>&1; then
  echo "metrics-server already installed"
else
  run kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
  # kind's kubelet serves a self-signed certificate
  run kubectl patch deployment metrics-server -n kube-system --type=json \
    -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
fi
run kubectl wait --for=condition=Available --timeout=300s -n kube-system deployment/metrics-server
echo "waiting for the metrics API to serve data ..."
for _ in $(seq 1 30); do kubectl top nodes >/dev/null 2>&1 && break; sleep 5; done
pause

step "5/8  Deploy the operator (CRD + RBAC + webhooks)"
run make deploy IMG="$IMG"
run kubectl wait --for=condition=Available --timeout=300s \
  -n webapp-operator-system deployment/webapp-operator-controller-manager
pause

fi  # end of setup phase

step "6/8  Create a WebApp — one resource in, a full stack out"
run kubectl apply -f config/samples/platform_v1_webapp.yaml
run kubectl wait --for=condition=Available --timeout=180s deployment/webapp-sample-deployment
run kubectl get webapp
# The HPA needs one sync cycle before it can report a utilization figure.
for _ in $(seq 1 24); do
  kubectl get hpa webapp-sample-autoscaler -o jsonpath='{.status.currentMetrics}' 2>/dev/null | grep -q averageUtilization && break
  sleep 5
done
run kubectl get deployment,service,hpa,pdb
pause

step "7/8  Self-healing — delete the Deployment and watch it come back"
run kubectl delete deployment webapp-sample-deployment
run kubectl wait --for=create --timeout=60s deployment/webapp-sample-deployment
run kubectl wait --for=condition=Available --timeout=120s deployment/webapp-sample-deployment
run kubectl get deployment
pause

step "8/8  Admission control — a mutable tag is rejected before it is stored"
echo "\$ kubectl apply -f config/samples/platform_v1_webapp_invalid.yaml"
if kubectl apply -f config/samples/platform_v1_webapp_invalid.yaml 2>&1; then
  echo "UNEXPECTED: the invalid WebApp was accepted" >&2
  exit 1
fi

printf '\n\033[1;32m✔ Demo complete.\033[0m  Tear down with:  kind delete cluster --name %s\n' "$CLUSTER"
