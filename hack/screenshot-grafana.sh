#!/usr/bin/env bash
# Captures docs/assets/grafana.png from a real Grafana instance.
#
# Brings up Prometheus + Grafana as containers, points Prometheus at a locally
# running operator, provisions the dashboard from config/grafana/, generates
# reconcile traffic so the panels have data, and screenshots the result with
# headless Chromium via the official Playwright image.
#
# Nothing is installed on the host: the browser, Node and its system libraries
# all live inside the container.
set -euo pipefail

OUT="docs/assets/grafana.png"
WORK="$(mktemp -d)"
METRICS_PORT="${METRICS_PORT:-8080}"
PLAYWRIGHT_IMAGE="${PLAYWRIGHT_IMAGE:-mcr.microsoft.com/playwright:v1.49.0-jammy}"
# Must match the image tag so playwright-core resolves the bundled browsers.
PLAYWRIGHT_NPM_VERSION="${PLAYWRIGHT_NPM_VERSION:-1.49.0}"

MANAGER_NS="webapp-operator-system"
MANAGER_DEPLOY="webapp-operator-controller-manager"

WEBHOOK_BACKUP="$(mktemp)"

cleanup() {
  docker rm -f webapp-docs-prometheus webapp-docs-grafana >/dev/null 2>&1 || true
  # Restoring matters: without these configurations the operator silently stops
  # enforcing its admission policy. Verify rather than hope, and say so if it
  # could not be done.
  if [[ -s "$WEBHOOK_BACKUP" ]] && grep -q 'kind: .*WebhookConfiguration' "$WEBHOOK_BACKUP"; then
    kubectl apply -f "$WEBHOOK_BACKUP" >/dev/null 2>&1 || true
    if ! kubectl get mutatingwebhookconfiguration,validatingwebhookconfiguration \
         --no-headers 2>/dev/null | grep -q .; then
      echo "WARNING: the admission webhooks could not be restored." >&2
      echo "         Reinstate them with:  make deploy IMG=<image>" >&2
    fi
  fi
  rm -f "$WEBHOOK_BACKUP"
  [[ -n "${OPERATOR_PID:-}" ]] && kill "$OPERATOR_PID" >/dev/null 2>&1 || true
  if [[ -n "${MANAGER_REPLICAS:-}" && "$MANAGER_REPLICAS" != "0" ]]; then
    kubectl scale deployment -n "$MANAGER_NS" "$MANAGER_DEPLOY" \
      --replicas="$MANAGER_REPLICAS" >/dev/null 2>&1 || true
  fi
  # npm inside the container writes as root; ignore what we cannot remove.
  rm -rf "$WORK" 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$(dirname "$OUT")" \
         "$WORK/provisioning/datasources" "$WORK/provisioning/dashboards" "$WORK/dashboards"

# A locally-run manager and a deployed one would both reconcile the same objects,
# fighting over every write. That inflates the update counters and the error
# ratio with conflicts, so the panels would picture a pathology created by this
# script rather than the operator's behaviour. Stand the deployed one down for
# the duration and restore it on exit.
# A manager left running by an earlier invocation would reconcile alongside this
# one, inflating both the update counters and the error ratio with write
# conflicts — the panels would then picture that fight rather than the operator.
# Check the port rather than a process name: kind runs the in-cluster manager on
# this host, so matching on "manager" would hit the operator's own pods.
if ss -ltn "sport = :${METRICS_PORT}" 2>/dev/null | grep -q LISTEN; then
  echo "error: another local manager is already running on :${METRICS_PORT}" >&2
  echo "       find it with:  ss -ltnp 'sport = :${METRICS_PORT}'" >&2
  exit 1
fi

MANAGER_REPLICAS="$(kubectl get deployment -n "$MANAGER_NS" "$MANAGER_DEPLOY" \
  -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")"
if [[ -n "$MANAGER_REPLICAS" && "$MANAGER_REPLICAS" != "0" ]]; then
  echo "==> standing down the in-cluster manager (${MANAGER_REPLICAS} replica(s)) for the capture"
  # The webhooks live in that manager and are registered failurePolicy: fail, so
  # with it down the API server rejects every WebApp write — which would leave
  # the dashboard with nothing to plot. Park them and restore them on exit.
  kubectl get validatingwebhookconfiguration,mutatingwebhookconfiguration \
    -o yaml >"$WEBHOOK_BACKUP" 2>/dev/null || true
  kubectl delete validatingwebhookconfiguration,mutatingwebhookconfiguration \
    --all >/dev/null 2>&1 || true
  kubectl scale deployment -n "$MANAGER_NS" "$MANAGER_DEPLOY" --replicas=0 >/dev/null
  kubectl wait --for=delete pod -l control-plane=controller-manager \
    -n "$MANAGER_NS" --timeout=60s >/dev/null 2>&1 || true
fi

echo "==> starting the operator with a plaintext metrics endpoint"
# Built and run directly rather than through `go run`: `go run` spawns the
# compiled binary as a child, so killing it on cleanup leaves that child holding
# the metrics port. A stale one then keeps answering scrapes, and the numbers
# describe a previous run instead of this one.
go build -o bin/manager ./cmd/main.go
ENABLE_WEBHOOKS=false ./bin/manager \
  --metrics-bind-address=":${METRICS_PORT}" --metrics-secure=false \
  --health-probe-bind-address=:8082 >"$WORK/operator.log" 2>&1 &
OPERATOR_PID=$!
curl --retry 40 --retry-delay 2 --retry-all-errors -sf "localhost:${METRICS_PORT}/metrics" >/dev/null

# On startup the manager reconciles every existing object at once. That burst is
# real but an order of magnitude above steady state, so it would stretch the y
# axis and flatten everything else. Let it age out of the graph window first.
kubectl delete webapp docs-demo --ignore-not-found >/dev/null 2>&1 || true

# Pre-existing WebApps get reconciled on startup too, and their counts would end
# up in the picture — a leftover load test would dominate the totals panel.
existing="$(kubectl get webapp -A --no-headers 2>/dev/null | wc -l)"
if (( existing > 0 )); then
  echo "error: ${existing} WebApp(s) already exist; the capture would show their activity" >&2
  echo "       remove them first:  kubectl delete webapp --all -A" >&2
  exit 1
fi

echo "==> letting the startup burst settle"
sleep 75

cat >"$WORK/prometheus.yml" <<EOF
global:
  scrape_interval: 5s
scrape_configs:
  - job_name: webapp-operator
    static_configs:
      - targets: ['localhost:${METRICS_PORT}']
EOF

cat >"$WORK/provisioning/datasources/ds.yml" <<'EOF'
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    uid: prometheus
    access: proxy
    url: http://localhost:9090
    isDefault: true
EOF

cat >"$WORK/provisioning/dashboards/provider.yml" <<'EOF'
apiVersion: 1
providers:
  - name: default
    type: file
    options:
      path: /var/lib/grafana/dashboards
EOF

# The committed dashboard uses a datasource *variable* so it can be imported
# anywhere; provisioning needs a concrete uid instead.
python3 - "$WORK/dashboards/webapp-operator.json" <<'PY'
import json, sys
dashboard = json.load(open('config/grafana/webapp-operator-dashboard.json'))
dashboard.pop('__inputs', None)
dashboard['templating'] = {'list': []}
open(sys.argv[1], 'w').write(json.dumps(dashboard).replace('${datasource}', 'prometheus'))
PY

echo "==> starting Prometheus and Grafana"
docker rm -f webapp-docs-prometheus webapp-docs-grafana >/dev/null 2>&1 || true
docker run -d --name webapp-docs-prometheus --network host \
  -v "$WORK/prometheus.yml:/etc/prometheus/prometheus.yml" prom/prometheus >/dev/null
docker run -d --name webapp-docs-grafana --network host \
  -e GF_AUTH_ANONYMOUS_ENABLED=true -e GF_AUTH_ANONYMOUS_ORG_ROLE=Admin \
  -e GF_AUTH_DISABLE_LOGIN_FORM=true \
  -v "$WORK/provisioning:/etc/grafana/provisioning" \
  -v "$WORK/dashboards:/var/lib/grafana/dashboards" \
  grafana/grafana >/dev/null
curl --retry 40 --retry-delay 2 --retry-all-errors -sf localhost:3000/api/health >/dev/null

echo "==> generating reconcile traffic so the panels are not empty"
# Alternating the image forces real create/update work on every pass, which is
# what gives the rate() panels a shape instead of a single spike at the edge.
for i in $(seq 1 24); do
  if (( i % 2 == 0 )); then TAG=1.27-alpine; else TAG=1.26-alpine; fi
  kubectl apply -f - >/dev/null 2>&1 <<EOF || true
apiVersion: platform.miportfolio.com/v1
kind: WebApp
metadata:
  name: docs-demo
spec:
  image: nginxinc/nginx-unprivileged:${TAG}
  port: 8080
  replicas: 2
  autoscaling: {minReplicas: 2, maxReplicas: 6, cpuThresholdPercent: 70}
EOF
  sleep 5
done

# A dashboard with nothing on it is not worth capturing; fail loudly instead.
if ! kubectl get webapp docs-demo >/dev/null 2>&1; then
  echo "error: the traffic WebApp was never created — nothing to plot" >&2
  exit 1
fi
sleep 15   # let Prometheus finish the last rate() window

echo "==> screenshotting with headless Chromium"
cat >"$WORK/shot.js" <<'EOF'
// playwright-core ships the API without downloading browsers; the image already
// provides them under /ms-playwright, and a matching version finds them there.
const { chromium } = require('/work/node_modules/playwright-core');

(async () => {
  const browser = await chromium.launch({
    args: ['--no-sandbox', '--disable-gpu', '--enable-unsafe-swiftshader'],
  });
  const page = await browser.newPage({
    viewport: { width: 1600, height: 1000 },
    // Grafana's bootstrap calls Intl APIs; with no locale configured the
    // container's browser throws "Incorrect locale information provided".
    locale: 'en-US',
    timezoneId: 'UTC',
  });
  page.on('console', m => console.log('[page]', m.text()));

  await page.goto('http://localhost:3000/d/webapp-operator?from=now-3m&to=now&kiosk',
                  { waitUntil: 'networkidle' });

  // Prefer waiting for a real panel to have rendered. Grafana's internal DOM
  // changes between releases, so treat this as best-effort and fall back to a
  // fixed settle time rather than failing the whole capture.
  try {
    await page.getByText('Reconciles per second', { exact: false }).first()
              .waitFor({ state: 'visible', timeout: 30000 });
  } catch {
    console.log('panel title not found; capturing anyway');
  }
  await page.waitForTimeout(8000);

  // Never overwrite a good asset with a broken capture: if Grafana rendered its
  // "failed to load application files" fallback, or no panel is present, bail
  // out with a non-zero exit instead of writing the file.
  const body = await page.textContent('body');
  // "No data" panels render fine structurally, so the earlier structural check
  // was not enough: a capture with empty panels is worse than no capture.
  const looksBroken = !body
    || body.includes('failed to load its application files')
    || body.includes('No data');
  const hasPanel = await page.locator('[data-testid="data-testid panel content"], .panel-content, .u-plot, canvas')
                             .count();
  if (looksBroken || hasPanel === 0) {
    console.error('capture rejected: dashboard did not render data (panels: ' + hasPanel + ')');
    await page.screenshot({ path: '/work/rejected.png' });
    await browser.close();
    process.exit(2);
  }

  await page.screenshot({ path: '/out/grafana.tmp.png', fullPage: false });
  await browser.close();
})();
EOF

docker run --rm --network host \
  -v "$WORK:/work" -v "$(pwd)/$(dirname "$OUT"):/out" \
  --user "$(id -u):$(id -g)" -e HOME=/work \
  -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 \
  -w /work "$PLAYWRIGHT_IMAGE" \
  bash -lc "npm install --no-save --silent playwright-core@${PLAYWRIGHT_NPM_VERSION} && node /work/shot.js"

mv "$(dirname "$OUT")/grafana.tmp.png" "$OUT"
kubectl delete webapp docs-demo --ignore-not-found >/dev/null 2>&1 || true
printf '\n✔ %s (%s)\n' "$OUT" "$(du -h "$OUT" | cut -f1)"
