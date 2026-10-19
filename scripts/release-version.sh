#!/usr/bin/env bash
# release-version.sh VERSION: stamp one release version into every file that
# carries it (Helm charts, chart values, Zarf package, image labels) so the
# binaries, images, charts and Zarf package of a release all agree.
#
# Used by .github/workflows/release.yaml; run locally before tagging:
#   scripts/release-version.sh 0.3.0 && git diff
set -euo pipefail
VERSION=${1:?usage: release-version.sh VERSION (without leading v)}
VERSION=${VERSION#v}
case "$VERSION" in
*[!0-9A-Za-z.+-]*) echo "invalid version: $VERSION" >&2; exit 1 ;;
esac
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

CURRENT=$(sed -n 's/^version: //p' charts/gofi/Chart.yaml)
[ -n "$CURRENT" ] || { echo "cannot read current chart version" >&2; exit 1; }
echo "stamping $CURRENT -> $VERSION"
esc() { printf '%s' "$1" | sed 's/[.[\*^$]/\\&/g'; }
CUR=$(esc "$CURRENT")

sed -i "s/^version: $CUR\$/version: $VERSION/; s/^appVersion: \"$CUR\"\$/appVersion: \"$VERSION\"/" charts/gofi/Chart.yaml
sed -i "s/^appVersion: \"$CUR\"\$/appVersion: \"$VERSION\"/" chart/Chart.yaml
sed -i "s/^\(  *tag: \)\"$CUR\"\$/\1\"$VERSION\"/" charts/gofi/values.yaml values/*.yaml
sed -i -E "s#(ghcr\.io/wcrum/gofi[a-z-]*):$CUR([^0-9A-Za-z.+-]|\$)#\1:$VERSION\2#g" zarf.yaml python/Dockerfile
sed -i "s/^\(        version: \)$CUR\$/\1$VERSION/" common/zarf.yaml
sed -i "s/^  version: dev\$/  version: $VERSION/; s/^  version: $CUR\$/  version: $VERSION/" zarf.yaml
sed -i "s/org.opencontainers.image.version=\"$CUR\"/org.opencontainers.image.version=\"$VERSION\"/" Dockerfile python/Dockerfile
sed -i "s/^\(    ref: \)dev\$/\1$VERSION/; s/^\(  version: \)dev\$/\1$VERSION/" bundle/uds-bundle.yaml

if grep -rn --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=ui -e "$CUR" charts/gofi/Chart.yaml charts/gofi/values.yaml values zarf.yaml common/zarf.yaml Dockerfile python/Dockerfile | grep -v "$VERSION" | grep -q .; then
	echo "warning: some occurrences of $CURRENT were not rewritten:" >&2
	grep -rn --exclude-dir=.git -e "$CUR" charts/gofi/Chart.yaml charts/gofi/values.yaml values zarf.yaml common/zarf.yaml Dockerfile python/Dockerfile | grep -v "$VERSION" >&2 || true
fi
echo "done"
