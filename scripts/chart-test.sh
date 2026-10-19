#!/usr/bin/env bash
# Chart gates: lint both charts, render every values overlay, run the
# helm-unittest suites under charts/gofi/tests, and lint the Zarf package.
# Uses helm from PATH, else the alpine/helm and helmunittest/helm-unittest
# images (HELM_IMAGE / UNITTEST_IMAGE override the tags).
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
HELM_IMAGE=${HELM_IMAGE:-alpine/helm:3.19.0}
UNITTEST_IMAGE=${UNITTEST_IMAGE:-helmunittest/helm-unittest:3.19.0-1.0.1}
out=${CHART_TEST_OUT:-$(mktemp -d)}
mkdir -p "$out"
failed=0

if command -v helm >/dev/null 2>&1; then
  helm() { command helm "$@"; }
else
  helm() { docker run --rm -v "$root":/src:ro -w /src "$HELM_IMAGE" "$@"; }
fi

step() { printf '\n== %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; failed=1; }

step "helm lint --strict"
helm lint charts/gofi --strict
helm lint chart --strict

step "render every values overlay"
render() {
  local name=$1; shift
  if ! helm template gofi charts/gofi --namespace gofi "$@" >"$out/$name.yaml"; then
    fail "render $name"
    return
  fi
  printf '%s: %s objects\n' "$name" "$(grep -c '^kind:' "$out/$name.yaml")"
}
render defaults
render uds-upstream -f values/common-values.yaml -f values/upstream-values.yaml
render production -f values/production-values.yaml
render dev -f values/dev-values.yaml
render everything \
  --set alerting.enabled=true --set networkPolicy.enabled=true --set priorityClasses.create=true \
  --set replicaCount=3 --set leaderElection.enabled=true --set podAntiAffinity=hard \
  --set workers.replicas=2 --set workers.autoscaling.enabled=true --set 'workers.autoscaling.libraries={shared}' \
  --set toolingImage.repository=ghcr.io/wcrum/gofi-python-tooling --set service.type=LoadBalancer
helm template uds-gofi-config chart --namespace gofi >"$out/uds-config.yaml"
helm template uds-gofi-config chart --namespace gofi -f chart/zarf-values.yaml --set domain=uds.dev >"$out/uds-config-zarf.yaml"

step "no imagePullPolicy Always outside the dev overlay"
for f in defaults uds-upstream production everything; do
  if grep -q 'imagePullPolicy: Always' "$out/$f.yaml"; then
    fail "$f renders imagePullPolicy: Always"
  fi
done
grep -q 'imagePullPolicy: Always' "$out/dev.yaml" || fail "dev overlay should pull Always"
for f in values/upstream-values.yaml values/production-values.yaml values/common-values.yaml charts/gofi/values.yaml; do
  if grep -E '^\s*pullPolicy:\s*Always' "$f" >/dev/null; then
    fail "$f sets pullPolicy: Always"
  fi
done

step "production overlay renders HA objects"
grep -q '^kind: PodDisruptionBudget' "$out/production.yaml" || fail "production: no PodDisruptionBudget"
grep -q '^kind: PriorityClass' "$out/production.yaml" || fail "production: no PriorityClass"
grep -q '^kind: NetworkPolicy' "$out/production.yaml" || fail "production: no NetworkPolicy"
grep -q 'type: RollingUpdate' "$out/production.yaml" || fail "production: not RollingUpdate"
grep -q 'resources: \["leases"\]' "$out/production.yaml" || fail "production: no Lease RBAC"
grep -q 'startupProbe:' "$out/defaults.yaml" || fail "defaults: no startupProbe"
grep -q 'name: GOMEMLIMIT' "$out/defaults.yaml" || fail "defaults: no GOMEMLIMIT"

step "helm-unittest"
if helm plugin list 2>/dev/null | grep -q '^unittest'; then
  helm unittest -f 'tests/*_test.yaml' charts/gofi || fail "helm unittest"
else
  docker run --rm -v "$root":/apps "$UNITTEST_IMAGE" -f 'tests/*_test.yaml' charts/gofi || fail "helm unittest"
fi

step "zarf dev lint"
if command -v zarf >/dev/null 2>&1; then
  zarf dev lint . --flavor upstream || fail "zarf dev lint"
elif command -v uds >/dev/null 2>&1; then
  uds zarf dev lint . --flavor upstream || fail "zarf dev lint"
else
  echo "zarf not installed; skipped"
fi

printf '\nrendered manifests: %s\n' "$out"
if [ "$failed" -ne 0 ]; then
  echo "chart tests FAILED" >&2
  exit 1
fi
echo "chart tests passed"
