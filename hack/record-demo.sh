#!/usr/bin/env bash
# Records the terminal demo and renders it as docs/assets/demo.gif.
#
# Tooling is downloaded as static binaries into ./bin (the same convention the
# Makefile uses for controller-gen and kustomize), so nothing is installed
# system-wide and no root is required.
#
#   make record-demo            # prepare a cluster, record, render
#   SKIP_PREPARE=1 make record-demo   # reuse the cluster already installed
set -euo pipefail

ASCIINEMA_VERSION="${ASCIINEMA_VERSION:-v3.2.1}"
AGG_VERSION="${AGG_VERSION:-v1.9.0}"
CLUSTER="${CLUSTER:-webapp-demo}"
IMG="${IMG:-controller:demo}"
OUT_DIR="docs/assets"
# TARGET=demo        -> the getting-started walkthrough (hack/demo.sh)
# TARGET=showcase    -> the capability showcase        (hack/showcase.sh)
# TARGET=autoscaling -> autoscaling under load         (hack/demo-autoscaling.sh)
TARGET="${TARGET:-demo}"
CAST="${OUT_DIR}/${TARGET}.cast"
GIF="${OUT_DIR}/${TARGET}.gif"

mkdir -p bin "$OUT_DIR"

fetch() { # fetch <name> <url>
  if [[ ! -x "bin/$1" ]]; then
    echo "downloading $1 ..."
    curl -fLso "bin/$1" "$2"
    chmod +x "bin/$1"
  fi
}
fetch asciinema "https://github.com/asciinema/asciinema/releases/download/${ASCIINEMA_VERSION}/asciinema-x86_64-unknown-linux-musl"
fetch agg        "https://github.com/asciinema/agg/releases/download/${AGG_VERSION}/agg-x86_64-unknown-linux-musl"

# The recording shows how the operator is *used*. Cluster creation, the image
# build and the cert-manager install are prerequisites, not part of the story,
# so they run before the recorder starts.
if [[ "${SKIP_PREPARE:-0}" != "1" ]]; then
  echo "==> preparing the cluster (not recorded)"
  # Errors are deliberately NOT swallowed: recording against a half-prepared or
  # stale cluster produces a GIF that shows the wrong thing.
  CLUSTER="$CLUSTER" IMG="$IMG" ./hack/demo.sh
  echo "==> cluster ready"
fi

# The prepare phase leaves a WebApp behind; remove it so the recording shows a
# real creation ("created") rather than a no-op ("unchanged").
kubectl delete -f config/samples/platform_v1_webapp.yaml --ignore-not-found >/dev/null 2>&1 || true

echo "==> recording"
case "$TARGET" in
  showcase)    RECORD_COMMAND="./hack/showcase.sh" ;;
  autoscaling) RECORD_COMMAND="./hack/demo-autoscaling.sh" ;;
  *)           RECORD_COMMAND="./hack/demo.sh" ;;
esac

DEMO_SKIP_SETUP=1 CLUSTER="$CLUSTER" IMG="$IMG" PAUSE="${PAUSE:-1}" \
  ./bin/asciinema record "$CAST" \
    --overwrite \
    --idle-time-limit 1.5 \
    --command "$RECORD_COMMAND"

echo "==> rendering $GIF"
# A generous font size keeps kubectl tables readable once the GIF is scaled down
# in the README; --speed trims the waits for pods to become ready.
./bin/agg \
  --font-size 15 \
  --speed 1.6 \
  --theme asciinema \
  --idle-time-limit 1.5 \
  "$CAST" "$GIF"

printf '\n✔ %s (%s)\n' "$GIF" "$(du -h "$GIF" | cut -f1)"
