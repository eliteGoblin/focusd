#!/usr/bin/env bash
# Build the focusd protection platform for the supported OS/arch matrix.
# CGO-free (modernc.org/sqlite) so cross-compilation is trivial.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${ROOT}/dist"
mkdir -p "${OUT}"

VERSION="${VERSION:-$(git -C "${ROOT}" describe --tags --always --dirty 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=${VERSION}"

matrix=(
  "darwin arm64"
  "darwin amd64"
  "windows amd64"
)

cd "${ROOT}/platform"
for entry in "${matrix[@]}"; do
  read -r goos goarch <<<"${entry}"
  ext=""
  [ "${goos}" = "windows" ] && ext=".exe"
  bin="${OUT}/focusd-platform-${goos}-${goarch}${ext}"
  echo "building ${bin}"
  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${bin}" ./cmd/platform
done

# Linux has sha256sum; macOS has shasum. Pick whichever exists.
if command -v sha256sum >/dev/null 2>&1; then SHACMD="sha256sum"
else SHACMD="shasum -a 256"; fi
( cd "${OUT}" && ${SHACMD} focusd-platform-* > platform-checksums.txt )
echo "platform artifacts in ${OUT}"
