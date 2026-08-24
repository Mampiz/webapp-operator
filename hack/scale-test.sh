#!/usr/bin/env bash
# Measures how the operator behaves as the number of WebApps grows.
#
# Creates N WebApps, waits for the work queue to drain, and reads the result
# straight from the manager's own metrics endpoint:
#   - reconcile latency p50 / p95   (controller_runtime_reconcile_time_seconds)
#   - work queue depth              (workqueue_depth)
#   - manager CPU and memory        (process_* metrics)
#
# Usage:  ./hack/scale-test.sh 100 500
# Requires a cluster with the CRD installed and NO other manager running, since
# two reconcilers would contend and invalidate the numbers.
set -euo pipefail

NS="${NS:-scale-test}"
IMAGE="${IMAGE:-nginxinc/nginx-unprivileged:1.27-alpine}"
METRICS_PORT="${METRICS_PORT:-8080}"
SIZES=("$@")
[[ ${#SIZES[@]} -eq 0 ]] && SIZES=(100)

if pgrep -f "exe/main --metrics-bind-address=:${METRICS_PORT}" >/dev/null 2>&1; then
  echo "error: a manager is already running on :${METRICS_PORT}" >&2
  exit 1
fi

MANAGER_NS="webapp-operator-system"
MANAGER_DEPLOY="webapp-operator-controller-manager"
WEBHOOK_BACKUP="$(mktemp)"

cleanup() {
  [[ -n "${OPERATOR_PID:-}" ]] && kill "$OPERATOR_PID" >/dev/null 2>&1 || true
  kubectl delete namespace "$NS" --wait=false >/dev/null 2>&1 || true
  if [[ -s "$WEBHOOK_BACKUP" ]]; then
    kubectl apply -f "$WEBHOOK_BACKUP" >/dev/null 2>&1 || true
  fi
  rm -f "$WEBHOOK_BACKUP"
  if [[ -n "${MANAGER_REPLICAS:-}" && "$MANAGER_REPLICAS" != "0" ]]; then
    kubectl scale deployment -n "$MANAGER_NS" "$MANAGER_DEPLOY" \
      --replicas="$MANAGER_REPLICAS" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

MANAGER_REPLICAS="$(kubectl get deployment -n "$MANAGER_NS" "$MANAGER_DEPLOY" \
  -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")"
if [[ -n "$MANAGER_REPLICAS" && "$MANAGER_REPLICAS" != "0" ]]; then
  # The webhooks are served by that manager and are registered failurePolicy:
  # fail, so standing it down makes the API server reject every WebApp write —
  # correct behaviour, and fatal for a benchmark. Park the configurations and
  # restore them on exit. What is measured here is the reconciler, not admission.
  kubectl get validatingwebhookconfiguration,mutatingwebhookconfiguration \
    -l app.kubernetes.io/name=webapp-operator -o yaml >"$WEBHOOK_BACKUP" 2>/dev/null || true
  if [[ ! -s "$WEBHOOK_BACKUP" ]] || ! grep -q 'kind:' "$WEBHOOK_BACKUP"; then
    kubectl get validatingwebhookconfiguration,mutatingwebhookconfiguration \
      -o yaml 2>/dev/null \
      | grep -q 'webapp' && kubectl get validatingwebhookconfiguration,mutatingwebhookconfiguration \
      -o yaml >"$WEBHOOK_BACKUP" 2>/dev/null || true
  fi
  kubectl delete validatingwebhookconfiguration,mutatingwebhookconfiguration \
    --all >/dev/null 2>&1 || true

  kubectl scale deployment -n "$MANAGER_NS" "$MANAGER_DEPLOY" --replicas=0 >/dev/null
  kubectl wait --for=delete pod -l control-plane=controller-manager \
    -n "$MANAGER_NS" --timeout=60s >/dev/null 2>&1 || true
fi

# A previous run tears the namespace down asynchronously, so it may still be
# Terminating here — creating objects in it would fail. Wait it out, then create.
for _ in $(seq 1 60); do
  phase="$(kubectl get namespace "$NS" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  [[ "$phase" == "Terminating" ]] || break
  sleep 2
done
kubectl create namespace "$NS" >/dev/null 2>&1 || true
kubectl wait --for=jsonpath='{.status.phase}'=Active "namespace/$NS" --timeout=60s >/dev/null

echo "==> starting the manager"
ENABLE_WEBHOOKS=false go run ./cmd/main.go \
  --metrics-bind-address=":${METRICS_PORT}" --metrics-secure=false \
  --health-probe-bind-address=:8082 >/tmp/scale-operator.log 2>&1 &
OPERATOR_PID=$!
curl --retry 60 --retry-delay 2 --retry-all-errors -sf "localhost:${METRICS_PORT}/metrics" >/dev/null

metric() { # metric <promql-ish grep> ; prints the raw sample value
  curl -s "localhost:${METRICS_PORT}/metrics" | grep -E "^$1" | awk '{print $2}' | head -1
}

# Reconcile latency comes as a histogram; derive quantiles from the buckets.
quantile() { # quantile <q>
  curl -s "localhost:${METRICS_PORT}/metrics" \
    | grep '^controller_runtime_reconcile_time_seconds_bucket{controller="webapp"' \
    | sed -E 's/.*le="([^"]+)"} ([0-9.e+]+)/\1 \2/' \
    | sort -g \
    | awk -v q="$1" '
        { le[NR]=$1; c[NR]=$2; n=NR }
        END {
          total=c[n]; if (total==0) { print "n/a"; exit }
          target=q*total
          for (i=1;i<=n;i++) if (c[i]>=target) { printf "%s\n", le[i]; exit }
        }'
}

printf '\n%-8s %-10s %-10s %-12s %-10s %-10s\n' \
  "WEBAPPS" "P50" "P95" "QUEUE_MAX" "CPU_S" "RSS_MB"

created=0
for target in "${SIZES[@]}"; do
  echo "==> creating WebApps up to ${target}" >&2
  failed=0
  for ((i = created + 1; i <= target; i++)); do
    cat <<EOF | kubectl apply -f - >/dev/null 2>&1 || failed=$((failed + 1))
apiVersion: platform.miportfolio.com/v1
kind: WebApp
metadata: {name: scale-$i, namespace: $NS}
spec: {image: $IMAGE, port: 8080, replicas: 1}
EOF
  done
  created=$target
  (( failed > 0 )) && echo "warning: ${failed} of the applies failed" >&2

  # Wait for the queue to drain: that is the point where every WebApp has been
  # reconciled at least once, which is what the numbers should describe.
  queue_max=0
  for _ in $(seq 1 120); do
    depth=$(metric 'workqueue_depth\{[^}]*name="webapp"' || echo 0)
    depth=${depth:-0}
    depth_int=${depth%.*}
    (( depth_int > queue_max )) && queue_max=$depth_int
    [[ "$depth_int" == "0" ]] && sleep 3 && break
    sleep 1
  done

  printf '%-8s %-10s %-10s %-12s %-10s %-10s\n' \
    "$target" \
    "$(quantile 0.5)s" \
    "$(quantile 0.95)s" \
    "$queue_max" \
    "$(printf '%.1f' "$(metric 'process_cpu_seconds_total')")" \
    "$(awk -v b="$(metric 'process_resident_memory_bytes')" 'BEGIN{printf "%.0f", b/1048576}')"
done

echo ""
echo "WebApps created: $created in namespace $NS (deleted on exit)"
