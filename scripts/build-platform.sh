#!/usr/bin/env bash
# Build the focusd protection platform for the supported OS/arch matrix.
# CGO-free (modernc.org/sqlite) so cross-compilation is trivial.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${ROOT}/dist"
mkdir -p "${OUT}"

VERSION="${VERSION:-$(git -C "${ROOT}" describe --tags --always --dirty 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=${VERSION}"

# Rebundle every plugin BEFORE building the platform. The platform embeds
# each plugin's binary from platform/internal/bundle/data/<id>/ via
# //go:embed; if a plugin's source changed but its bundled binary wasn't
# refreshed, the platform ships a STALE plugin (this is exactly what caused
# the v0.12.0 -> v0.12.1 hotfix). Running each plugin's `make bundle` here
# keeps the embedded copies in lockstep with source on every build.
#
# CRITICAL: pin the bundle build to the real deploy target (darwin/arm64),
# NOT the host. This script's `build` job runs on ubuntu-latest in CI
# (.github/workflows/platform.yml); a host-arch `make bundle` there would
# overwrite the committed darwin/arm64 plugin binaries with LINUX ones and
# embed them into the darwin platform release — a broken release (Copilot
# review, PR #44). The platform embeds a single plugin binary into every
# matrix variant; only Apple Silicon macOS is a real deployment, so the
# embedded plugins target darwin/arm64 (matching the previously-committed
# binaries). Plugins are CGO-free, so this cross-compiles from any host.
export BUNDLE_GOOS="${BUNDLE_GOOS:-darwin}"
export BUNDLE_GOARCH="${BUNDLE_GOARCH:-arm64}"
echo "rebundling plugins (target ${BUNDLE_GOOS}/${BUNDLE_GOARCH})…"
for pdir in "${ROOT}"/plugins/*/; do
  name="$(basename "${pdir}")"
  [ "${name}" = "_fakes" ] && continue
  if [ -f "${pdir}Makefile" ] && grep -qE '^bundle:' "${pdir}Makefile"; then
    echo "  bundle ${name}"
    make -C "${pdir}" bundle GOOS="${BUNDLE_GOOS}" GOARCH="${BUNDLE_GOARCH}" >/dev/null
  else
    echo "  skip ${name} (no bundle target)"
  fi
done

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
