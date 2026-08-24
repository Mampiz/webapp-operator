#!/usr/bin/env bash
# Showcase: the capabilities that distinguish an operator from a set of manifests.
#
# Each scenario proves a claim this project makes, against a live cluster:
#   1. Defaulting        — a minimal spec is completed into a coherent one
#   2. One resource in   — a whole stack out
#   3. Drift correction  — a manual edit to a managed field is reverted
#   4. Self-healing      — deleted children come back
#   5. Honest status     — a broken image is reported, not hidden
#   6. Admission control — four classes of invalid input, four rejections
#   7. Cascade delete    — one delete removes everything
#
# Autoscaling under real CPU load is a separate scenario because it is bound by
# the HPA's own sync interval and takes minutes rather than seconds:
#   SHOWCASE_AUTOSCALING=1 ./hack/showcase.sh
#
# Assumes the operator, its webhooks and metrics-server are installed
# (hack/demo.sh does that). Run with: make showcase
set -euo pipefail

NS="${NS:-showcase}"
APP="${APP:-shop-api}"
IMAGE="${IMAGE:-nginxinc/nginx-unprivileged:1.27-alpine}"
OLD_IMAGE="${OLD_IMAGE:-nginxinc/nginx-unprivileged:1.26-alpine}"
PAUSE="${PAUSE:-1}"

step()  { printf '\n\033[1;36m━━━ %s\033[0m\n' "$1"; }
note()  { printf '\033[0;33m%s\033[0m\n' "$1"; }
run()   { printf '\033[0;90m$ %s\033[0m\n' "$*"; "$@"; }
pause() { sleep "$PAUSE"; }

kubectl create namespace "$NS" >/dev/null 2>&1 || true
kubectl config set-context --current --namespace="$NS" >/dev/null
trap 'kubectl config set-context --current --namespace=default >/dev/null 2>&1 || true' EXIT

# ─────────────────────────────────────────────────────────────────────────────
step "1/7  Defaulting — the webhook completes a minimal spec"
note "Submitting only an image and a port, with autoscaling enabled:"
cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: platform.miportfolio.com/v1
kind: WebApp
metadata: {name: $APP}
spec:
  image: $IMAGE
  port: 8080
  readinessPath: /
  autoscaling: {minReplicas: 2, maxReplicas: 8, cpuThresholdPercent: 50}
EOF
note "What was actually stored:"
kubectl get webapp "$APP" -o jsonpath='{"  replicas:     "}{.spec.replicas}{"\n  cpu request:  "}{.spec.resources.requests.cpu}{"\n  managed-by:   "}{.metadata.labels.app\.kubernetes\.io/managed-by}{"\n"}'
note "Neither replicas nor the CPU request was sent — the defaulting webhook added"
note "both. Without that request the HPA could never compute utilization at all."
pause

# ─────────────────────────────────────────────────────────────────────────────
step "2/7  One resource in, a full stack out"
kubectl wait --for=condition=Available --timeout=180s "deployment/${APP}-deployment" >/dev/null
run kubectl get webapp,deployment,service,hpa --no-headers
pause

# ─────────────────────────────────────────────────────────────────────────────
step "3/7  Drift correction — a manual edit to a managed field is reverted"
note "Someone patches the running Deployment by hand, bypassing the WebApp:"
run kubectl set image "deployment/${APP}-deployment" "webapp=${OLD_IMAGE}"
note "The desired state still says otherwise, so the next reconcile corrects it:"
for _ in $(seq 1 20); do
  current=$(kubectl get "deployment/${APP}-deployment" -o jsonpath='{.spec.template.spec.containers[0].image}')
  [[ "$current" == "$IMAGE" ]] && break
  sleep 2
done
printf '  edit applied:  %s\n  image now:     %s\n' "$OLD_IMAGE" "$current"
note "Level-based reconciliation: the operator compares against the spec, so it"
note "does not matter who changed what, or whether it saw the change happen."
pause

# ─────────────────────────────────────────────────────────────────────────────
step "4/7  Self-healing — delete what the operator owns"
run kubectl delete "deployment/${APP}-deployment" "service/${APP}-service"
for _ in $(seq 1 20); do
  kubectl get "deployment/${APP}-deployment" >/dev/null 2>&1 && break
  sleep 1
done
run kubectl get deployment,service --no-headers
note "Both were recreated without anyone touching the WebApp."
pause

# ─────────────────────────────────────────────────────────────────────────────
step "5/7  Honest status — a broken image is reported, not hidden"
run kubectl patch webapp "$APP" --type=merge -p '{"spec":{"image":"does-not-exist.invalid/nope:1.0"}}'
for _ in $(seq 1 20); do
  kubectl get webapp "$APP" -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' | grep -q False && break
  sleep 3
done
kubectl get webapp "$APP" -o jsonpath='{range .status.conditions[*]}  {.type}={.status} ({.reason}) {.message}{"\n"}{end}'
note "Rolling back to a good image:"
run kubectl patch webapp "$APP" --type=merge -p "{\"spec\":{\"image\":\"$IMAGE\"}}"
kubectl wait --for=condition=Available --timeout=180s "deployment/${APP}-deployment" >/dev/null
run kubectl get webapp "$APP"
pause

# ─────────────────────────────────────────────────────────────────────────────
step "6/7  Admission control — four kinds of invalid input"
try_apply() { # try_apply <description> <spec-body>
  printf '\033[0;90m# %s\033[0m\n' "$1"
  printf 'apiVersion: platform.miportfolio.com/v1\nkind: WebApp\nmetadata: {name: rejected}\nspec:\n%s\n' "$2" \
    | kubectl apply -f - 2>&1 | fmt -w 100 | sed 's/^/  /' || true
}
try_apply "mutable tag — rejected by the validating webhook" \
  "  image: nginx:latest
  port: 8080"
try_apply "port out of range — rejected by the OpenAPI schema" \
  "  image: nginx:1.27
  port: 70000"
try_apply "maxReplicas < minReplicas — rejected by a CEL rule" \
  "  image: nginx:1.27
  port: 8080
  autoscaling: {minReplicas: 9, maxReplicas: 3, cpuThresholdPercent: 70}"
try_apply "both PDB fields set — rejected by a CEL rule" \
  "  image: nginx:1.27
  port: 8080
  podDisruptionBudget: {minAvailable: 1, maxUnavailable: 1}"
note "None of these objects was ever stored."
pause

# ─────────────────────────────────────────────────────────────────────────────
if [[ "${SHOWCASE_AUTOSCALING:-0}" == "1" ]]; then
  step "Bonus  Autoscaling — real CPU load moves the replica count"
  note "Waiting for the metrics pipeline to report utilization ..."
  for _ in $(seq 1 24); do
    kubectl get hpa "${APP}-autoscaler" -o jsonpath='{.status.currentMetrics}' 2>/dev/null | grep -q averageUtilization && break
    sleep 5
  done
  run kubectl get hpa "${APP}-autoscaler" --no-headers
  note "Burning CPU inside every running pod ..."
  for pod in $(kubectl get pods -l "app=$APP" -o name); do
    # The background loops keep the exec stream open, so redirect their output and
  # cap the call: otherwise kubectl waits on a stream that never closes.
  timeout 10 kubectl exec "$pod" -- \
    sh -c 'for i in 1 2 3 4; do (while :; do :; done) >/dev/null 2>&1 & done' \
    >/dev/null 2>&1 || true
  done
  for _ in $(seq 1 18); do
    sleep 10
    kubectl get hpa "${APP}-autoscaler" --no-headers | awk '{printf "  cpu=%-14s replicas=%s\n", $4, $7}'
    [[ "$(kubectl get "deployment/${APP}-deployment" -o jsonpath='{.spec.replicas}')" -gt 2 ]] && break
  done
  run kubectl get pods -l "app=$APP" --no-headers
  note "The HPA scaled the Deployment; the operator never touched the replica count."
  pause
fi

# ─────────────────────────────────────────────────────────────────────────────
step "7/7  Cascade delete — one delete, nothing left behind"
run kubectl get deployment,service,hpa --no-headers
run kubectl delete webapp "$APP"
for _ in $(seq 1 20); do
  kubectl get "deployment/${APP}-deployment" >/dev/null 2>&1 || break
  sleep 1
done
run kubectl get deployment,service,hpa --no-headers
note "Owner references let Kubernetes garbage-collect the whole stack."

kubectl delete namespace "$NS" --wait=false >/dev/null 2>&1 || true
printf '\n\033[1;32m✔ Showcase complete.\033[0m\n'
