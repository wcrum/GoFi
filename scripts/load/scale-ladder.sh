#!/usr/bin/env bash
# Runs the scale ladder against a controller and prints the Markdown table.
#
#   scripts/load/scale-ladder.sh                       # binary on :9090, 10→100→500
#   GOFI_SERVER=http://127.0.0.1:19090 scripts/load/scale-ladder.sh 10,100
#   # Kubernetes (after k3d-up.sh + kwok-setup.sh):
#   source testenv/reports/k3d-gofi-e2e.env
#   scripts/load/scale-ladder.sh 10,100,500,1000 --nodeport \
#     --apiserver-metrics "$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')/metrics" \
#     --kubeconfig "$KUBECONFIG"
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LADDER="${1:-10,100,500}"
shift || true
cd "$ROOT"
GOWORK=off go run ./scripts/load/scaleladder --server "${GOFI_SERVER:-http://127.0.0.1:9090}" --ladder "$LADDER" "$@" | tee testenv/reports/scale-ladder.md
