#!/usr/bin/env bash
# Adds KWOK (Kubernetes WithOut Kubelet) fake nodes to a k3d cluster so the
# scale ladder can schedule hundreds of flow Pods without running them. Fake
# nodes are labelled type=kwok; GoFi's listener Pods carry no node selector,
# so taint the real agents or let the scheduler spread. Pods on KWOK nodes
# report Running without consuming memory, which is exactly what the
# control-plane ladder (API server QPS, controller RSS/CPU, sync time) needs;
# per-pod RSS is only meaningful on real nodes (scripts/load/pod-rss.sh).
#
#   scripts/load/kwok-setup.sh 50     # 50 fake nodes
set -euo pipefail

NODES="${1:-20}"
KWOK_VERSION="${KWOK_VERSION:-v0.6.1}"
: "${KUBECONFIG:?source the env file k3d-up.sh wrote}"

kubectl_() {
  if command -v kubectl >/dev/null 2>&1; then kubectl "$@"; else
    docker run --rm --network host -v "$KUBECONFIG:/kc:ro" -e KUBECONFIG=/kc -i bitnami/kubectl:latest "$@"; fi
}

kubectl_ apply -f "https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}/kwok.yaml"
kubectl_ apply -f "https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}/stage-fast.yaml"
kubectl_ -n kube-system rollout status deployment/kwok-controller --timeout=120s

for i in $(seq 0 $((NODES - 1))); do
  cat <<EOF | kubectl_ apply -f -
apiVersion: v1
kind: Node
metadata:
  name: kwok-node-$i
  labels:
    type: kwok
    kubernetes.io/hostname: kwok-node-$i
    kubernetes.io/os: linux
    kubernetes.io/role: agent
    node-role.kubernetes.io/agent: ""
  annotations:
    kwok.x-k8s.io/node: fake
    node.alpha.kubernetes.io/ttl: "0"
spec:
  taints:
    - key: kwok.x-k8s.io/node
      value: fake
      effect: NoSchedule
status:
  allocatable:
    cpu: "32"
    memory: 256Gi
    pods: "110"
  capacity:
    cpu: "32"
    memory: 256Gi
    pods: "110"
  nodeInfo:
    architecture: amd64
    kubeletVersion: fake
    operatingSystem: linux
EOF
done
echo "$NODES KWOK nodes created (taint kwok.x-k8s.io/node=fake:NoSchedule)."
echo "To place GoFi listener Pods on them, set GOFI_LISTENER_POD_TEMPLATE on the chart with a toleration, e.g."
echo '  --set-json controller.env=[{"name":"GOFI_LISTENER_POD_TEMPLATE","value":"{\"containers\":[]}"}]'
echo "or remove the taint: kubectl taint nodes -l type=kwok kwok.x-k8s.io/node-"
