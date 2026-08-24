#!/usr/bin/env bash
# Autoscaling under real CPU load, end to end.
#
# This is the scenario the operator's defaulting exists for: HPA target
# utilization is a percentage of the container's CPU *request*, so without one
# the autoscaler reports <unknown> and never acts. Nothing here sets a request —
# it is supplied for us — and the pods are then driven with genuine CPU load.
#
# Needs a cluster with the operator and metrics-server installed (hack/demo.sh).
set -euo pipefail

NS="${NS:-autoscaling-demo}"
APP="${APP:-shop-api}"
IMAGE="${IMAGE:-nginxinc/nginx-unprivileged:1.27-alpine}"

step() { printf '\n\033[1;36m━━━ %s\033[0m\n' "$1"; }
note() { printf '\033[0;33m%s\033[0m\n' "$1"; }
run()  { printf '\033[0;90m$ %s\033[0m\n' "$*"; "$@"; }

kubectl create namespace "$NS" >/dev/null 2>&1 || true
kubectl wait --for=jsonpath='{.status.phase}'=Active "namespace/$NS" --timeout=60s >/dev/null
kubectl config set-context --current --namespace="$NS" >/dev/null
trap 'kubectl delete namespace "$NS" --wait=false >/dev/null 2>&1 || true;
      kubectl config set-context --current --namespace=default >/dev/null 2>&1 || true' EXIT

step "1/4  A WebApp that asks to be autoscaled — note there are no resources set"
cat <<EOF | kubectl apply -f -
apiVersion: platform.miportfolio.com/v1
kind: WebApp
metadata: {name: $APP}
spec:
  image: $IMAGE
  port: 8080
  readinessPath: /
  autoscaling: {minReplicas: 2, maxReplicas: 8, cpuThresholdPercent: 50}
EOF
kubectl wait --for=condition=Available --timeout=180s "deployment/${APP}-deployment" >/dev/null

step "2/4  The CPU request was supplied, so the HPA has a denominator"
run kubectl get deployment "${APP}-deployment" \
  -o jsonpath='{"  cpu request: "}{.spec.template.spec.containers[0].resources.requests.cpu}{"\n"}'
note "Waiting for the metrics pipeline to report utilization ..."
for _ in $(seq 1 36); do
  kubectl get hpa "${APP}-autoscaler" -o jsonpath='{.status.currentMetrics}' 2>/dev/null \
    | grep -q averageUtilization && break
  sleep 5
done
run kubectl get hpa "${APP}-autoscaler" --no-headers

step "3/4  Driving real CPU load inside the pods"
for pod in $(kubectl get pods -l "app=$APP" -o name); do
  # The background loops keep the exec stream open, so redirect their output and
  # cap the call: otherwise kubectl waits on a stream that never closes.
  timeout 10 kubectl exec "$pod" -- \
    sh -c 'for i in 1 2 3 4; do (while :; do :; done) >/dev/null 2>&1 & done' \
    >/dev/null 2>&1 || true
done
note "The metrics pipeline samples every ~30s, so the load takes a moment to show."
note "Following the autoscaler:"
for _ in $(seq 1 30); do
  sleep 10
  line="$(kubectl get hpa "${APP}-autoscaler" --no-headers)"
  echo "$line" | awk '{printf "  cpu=%-14s replicas=%s\n", $4, $7}'
  # Only stop once utilization is genuinely above target and the autoscaler has
  # acted on it. Breaking on the replica count alone catches scale-ups the HPA
  # performs for other reasons (a min-replica correction, unready pods) and ends
  # the demo before the load it is supposed to be showing ever registers.
  cpu="$(echo "$line" | grep -oE 'cpu: [0-9]+%' | grep -oE '[0-9]+' || echo 0)"
  replicas="$(kubectl get "deployment/${APP}-deployment" -o jsonpath='{.spec.replicas}')"
  if [[ "${cpu:-0}" -gt 100 && "$replicas" -gt 2 ]]; then
    note "Utilization reached ${cpu}% of the request; scaled to ${replicas} replicas."
    break
  fi
done

step "4/4  What the autoscaler decided, in its own words"
kubectl describe hpa "${APP}-autoscaler" | sed -n '/Events:/,$p' | head -8
run kubectl get webapp,hpa --no-headers
note "The operator never wrote the replica count: with autoscaling on, that field"
note "belongs to the HPA. One writer per field."
