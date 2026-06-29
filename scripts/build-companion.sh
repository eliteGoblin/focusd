#!/usr/bin/env bash
# Build the focusd out-of-band companion (FEATURE 18 / ADR-0020) and STAGE it at
# the daemon's embed path so the daemon can stand the companion up offline.
#
# The daemon embeds the companion via //go:embed
# (daemon/internal/osadapter/companiondata/companion). In-repo that path holds a
# tiny PLACEHOLDER so the tree compiles; a RELEASE build must run THIS script
# FIRST to replace it with the real companion binary, THEN build (and sign) the
# daemon. Order matters:
#
#     scripts/build-companion.sh      # stage the real companion at the embed path
#     go build ./daemon/cmd/daemon    # build the daemon (embeds the companion)
#     # sign the daemon release  <-- the COMPANION is deliberately NOT mesh-signed
#
# CGO-free (modernc.org/sqlite is platform-side; the companion itself is pure Go),
# so this cross-compiles from any host. Default target is the only real deploy
# (darwin/arm64); override with COMPANION_GOOS / COMPANION_GOARCH.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOOS="${COMPANION_GOOS:-darwin}"
GOARCH="${COMPANION_GOARCH:-arm64}"
EMBED="${ROOT}/daemon/internal/osadapter/companiondata/companion"

echo "building companion (${GOOS}/${GOARCH}) -> ${EMBED}"
cd "${ROOT}/daemon"
CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
  go build -trimpath -ldflags "-s -w" -o "${EMBED}" ./cmd/companion

echo "companion staged for embedding; now build (and sign) the daemon."
