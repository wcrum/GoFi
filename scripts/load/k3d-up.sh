#!/usr/bin/env bash
# Creates a throwaway k3d cluster for GoFi e2e/scale runs (3 agents), imports
# the GoFi image, installs the chart, and prints the environment the e2e
# harness needs. Never touches an existing cluster: the name defaults to
# gofi-e2e and must not be "uds".
#
#   scripts/load/k3d-up.sh                 # create + install
#   scripts/load/k3d-up.sh down            # delete the cluster
#
# Requirements: docker, k3d (>= 5.6). kubectl and helm are run from
# containers when not installed (bitnami/kubectl, alpine/helm).
set -euo pipefail

CLUSTER="${GOFI_E2E_CLUSTER:-gofi-e2e}"
IMAGE="${GOFI_E2E_IMAGE:-ghcr.io/wcrum/gofi:0.2.0}"
NAMESPACE="${GOFI_E2E_NAMESPACE:-gofi}"
AGENTS="${GOFI_E2E_AGENTS:-3}"
API_NODEPORT="${GOFI_E2E_API_NODEPORT:-30090}"
# NodePort range published through the k3d load balancer for flow Services.
FLOW_PORTS="${GOFI_E2E_FLOW_PORTS:-31000-31099}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${GOFI_E2E_OUT:-$ROOT/testenv/reports/k3d-$CLUSTER.env}"

if [ "$CLUSTER" = "uds" ]; then
  echo "refusing to manage the shared 'uds' cluster" >&2
  exit 1
fi

kubeconfig_path() { k3d kubeconfig write "$CLUSTER"; }

kubectl_() {
  if command -v kubectl >/dev/null 2>&1; then
    kubectl --kubeconfig "$KUBECONFIG" "$@"
  else
    docker run --rm --network host -v "$KUBECONFIG:/kc:ro" -e KUBECONFIG=/kc \
      -v "$ROOT:$ROOT:ro" -w "$ROOT" bitnami/kubectl:latest "$@"
  fi
}

helm_() {
  if command -v helm >/dev/null 2>&1; then
    helm --kubeconfig "$KUBECONFIG" "$@"
  else
    docker run --rm --network host -v "$KUBECONFIG:/kc:ro" -e KUBECONFIG=/kc \
      -v "$ROOT:$ROOT:ro" -w "$ROOT" alpine/helm:3.16.4 "$@"
  fi
}

if [ "${1:-}" = "down" ]; then
  k3d cluster delete "$CLUSTER"
  rm -f "$OUT"
  exit 0
fi

if ! k3d cluster list -o json | grep -q "\"name\":\"$CLUSTER\""; then
  k3d cluster create "$CLUSTER" --agents "$AGENTS" \
    -p "$API_NODEPORT:$API_NODEPORT@loadbalancer" \
    -p "$FLOW_PORTS:$FLOW_PORTS@loadbalancer" \
    --k3s-arg "--disable=traefik@server:0" \
    --wait --timeout 180s
fi
export KUBECONFIG
KUBECONFIG="$(kubeconfig_path)"
k3d image import -c "$CLUSTER" "$IMAGE"

kubectl_ get ns "$NAMESPACE" >/dev/null 2>&1 || kubectl_ create ns "$NAMESPACE"
kubectl_ apply -f "$ROOT/charts/gofi/crds"
# Chart install: NodePort Service for the controller so the harness reaches it
# through the load balancer; image pull policy Never (imported above).
helm_ upgrade --install gofi "$ROOT/charts/gofi" -n "$NAMESPACE" \
  --set image.repository="${IMAGE%%:*}" --set image.tag="${IMAGE##*:}" \
  --set image.pullPolicy=Never \
  --set service.type=NodePort --set service.nodePort="$API_NODEPORT" \
  ${GOFI_E2E_HELM_ARGS:-} --wait --timeout 5m

mkdir -p "$(dirname "$OUT")"
cat >"$OUT" <<EOF
export KUBECONFIG=$KUBECONFIG
export GOFI_E2E_MODE=kubernetes
export GOFI_E2E_NAMESPACE=$NAMESPACE
export GOFI_E2E_API=http://127.0.0.1:$API_NODEPORT
export GOFI_E2E_NODE_HOST=127.0.0.1
export GOFI_E2E_HOST_ALIAS=host.k3d.internal
export GOFI_E2E_IMAGE=$IMAGE
EOF
echo "cluster $CLUSTER ready; source $OUT then: go test -tags e2e ./tests/e2e/"
