#!/usr/bin/env bash
# Build focusd plugins independently from the platform. Each plugin is a
# self-contained executable packaged with its plugin.json + checksums.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${ROOT}/dist"
PLUGINS_DIR="${ROOT}/plugins"

matrix=(
  "darwin arm64"
  "darwin amd64"
  "windows amd64"
)

for pdir in "${PLUGINS_DIR}"/*/; do
  name="$(basename "${pdir}")"
  [ "${name}" = "_fakes" ] && continue
  [ -f "${pdir}/plugin.json" ] || { echo "skip ${name}: no plugin.json"; continue; }

  VERSION="${VERSION:-$(git -C "${ROOT}" describe --tags --always --dirty 2>/dev/null || echo dev)}"
  LDFLAGS="-s -w -X main.version=${VERSION}"
  pkg="${OUT}/${name}"
  rm -rf "${pkg}"
  mkdir -p "${pkg}/bin"
  cp "${pdir}/plugin.json" "${pkg}/plugin.json"

  cd "${pdir}"
  for entry in "${matrix[@]}"; do
    read -r goos goarch <<<"${entry}"
    ext=""
    [ "${goos}" = "windows" ] && ext=".exe"
    dest="${pkg}/bin/${goos}-${goarch}"
    mkdir -p "${dest}"
    echo "building ${name} ${goos}/${goarch}"
    CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
      go build -trimpath -ldflags "${LDFLAGS}" -o "${dest}/${name}${ext}" ./cmd
  done

  ( cd "${pkg}" && find bin -type f | sort | xargs shasum -a 256 > checksums.txt )
  echo "packaged ${name} -> ${pkg}"
done
