#!/usr/bin/env bash
# Writes "pod bytes" lines of resident memory for GoFi listener Pods in a
# namespace (metrics-server / kubectl top), for scaleladder --pod-metrics.
#   scripts/load/pod-rss.sh gofi > /tmp/pods.txt
set -euo pipefail
NS="${1:-gofi}"
kubectl_() {
  if command -v kubectl >/dev/null 2>&1; then kubectl "$@"; else
    docker run --rm --network host -v "$KUBECONFIG:/kc:ro" -e KUBECONFIG=/kc bitnami/kubectl:latest "$@"; fi
}
kubectl_ top pod -n "$NS" -l gofi.io/managed-by=true --no-headers 2>/dev/null | awk '
  { v=$3; n=v; sub(/[A-Za-z]+$/, "", n); u=v; sub(/^[0-9.]+/, "", u);
    m=1; if (u=="Ki") m=1024; else if (u=="Mi") m=1048576; else if (u=="Gi") m=1073741824;
    printf "%s %d\n", $1, n*m }'
