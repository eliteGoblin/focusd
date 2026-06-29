//go:build darwin

package osadapter

import _ "embed"

// companionBinary is the out-of-band companion executable, embedded so the
// daemon can stand the companion up OFFLINE (FEATURE 18 / ADR-0020).
//
// The in-repo file at companiondata/companion is a PLACEHOLDER so the tree
// compiles; a real RELEASE build REPLACES it with the built `cmd/companion`
// binary BEFORE compiling the daemon (see scripts/build-companion.sh). Order
// matters: build+stage companion → build daemon → SIGN daemon. The companion is
// deliberately NOT signed with the mesh key (HARD INVARIANT #1).
//
// EnsureCompanion gates on companionMinBytes, so the tiny placeholder is never
// written to disk or loaded into launchd — until the real bytes are embedded the
// rail only scaffolds its folder/backup/heartbeat.
//
//go:embed companiondata/companion
var companionBinary []byte
