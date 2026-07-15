package relocate

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// FEATURE 24 (HF4) — disguise the PLATFORM child's process + on-disk identity.
//
// F19 moved the DAEMON MESH role off argv into env and gave the daemon binary a
// random basename. HF4 extends the same idea to the platform the daemon
// supervises, which still leaked three greppable tokens in `ps`:
//
//	.../bin/<v>/platform --workdir <workdir>
//	           ^version   ^literal  ^workdir path (twice)
//
// The fixes (see the store / platformsvc / status changes):
//   - the platform binary is stored under a per-install DISGUISED basename
//     (PlatformBinBase) instead of bin/<v>/platform — no version, no 'platform';
//   - the child's argv[0] is a generic token (PlatformArgv0) — not a path, not
//     'platform', not a version;
//   - --workdir moves OFF argv into an env var (the process list never shows it).
//
// Both tokens are DERIVED DETERMINISTICALLY from the per-install salt so the
// daemon's own status introspection (status/procCount) can reconstruct the exact
// argv it must match against `pgrep`, WITHOUT persisting a second lookup table.
// The salt is the "version→path index the daemon reads": salt + version → path,
// unguessable without the salt (which lives 0700 in the disguised daemon-home),
// yet stable across restarts and readable by every daemon role.
//
// Honest limit (documented, casual-grade): argv[0] and the executable path are
// always root-visible via /proc-equivalents; a determined/AI reader still learns
// them. The goal is only that nothing GREPPABLE (v0.16, 'platform', the workdir)
// maps to the install in a casual `ps aux | grep` sweep.

// procTokens is the disjoint pool for the platform child's argv[0]. Generic,
// lowercase, single-token helper/agent basenames that read as any of the dozens
// of background helpers on a normal Mac — deliberately DISJOINT from every mesh
// label pool (roster_styles.go) and the daemon binary pool (binWords) so
// "find one, grep for the rest" gains nothing.
var procTokens = []string{
	"syncagent", "prefsync", "cachehelper", "idxworker", "netagent",
	"mediahelper", "cloudsync", "authagent", "updatehelper", "assetworker",
	"streamhelper", "sessionagent", "trustagent", "lochelper", "diaghelper",
	"metricsagent", "reportagent", "indexhelper", "storeagent", "vaultagent",
	"pushhelper", "relayagent", "brokerhelper", "gatewayagent", "resolveragent",
	"scheduleragent", "dispatchhelper", "observeragent", "registryhelper", "snapshotagent",
}

// platBinWords is the disjoint pool for the platform binary's on-disk basename.
// Generic data/artifact nouns (a separate set from binWords) so the file in the
// platform-workdir bin dir carries no 'platform'/version token.
var platBinWords = []string{
	"catalog", "dataset", "resource", "fragment", "snapshot", "module",
	"package", "container", "volume", "record", "element", "artifact",
	"component", "asset", "capsule", "dossier", "profile", "registry",
	"chunk", "slice", "vector", "matrix", "channel", "cluster",
}

// hashSeed is the deterministic derivation primitive — sha256 of the salted seed.
func hashSeed(seed string) [32]byte { return sha256.Sum256([]byte(seed)) }

// pickDet deterministically selects a pool entry from a salted seed. Same seed →
// same entry on every process/host, so the daemon and its status subcommand agree
// on the disguised token without sharing state.
func pickDet(s []string, seed string) string {
	h := hashSeed(seed)
	n := binary.BigEndian.Uint32(h[:4])
	return s[int(n)%len(s)]
}

// randHexDet returns n deterministic hex bytes (2n chars) from a salted seed.
func randHexDet(seed string, n int) string {
	if n > 32 {
		n = 32
	}
	h := hashSeed(seed)
	return hex.EncodeToString(h[:n])
}

// PlatformArgv0 is the deterministic disguised argv[0] token for the platform
// child, derived from the per-install salt. Empty salt ⇒ "" (the caller keeps the
// legacy, non-disguised argv so existing tests / dev runs are unchanged).
func PlatformArgv0(salt string) string {
	if salt == "" {
		return ""
	}
	return pickDet(procTokens, salt+"|platform-argv0")
}

// DaemonArgv0 is the deterministic disguised argv[0] token for a daemon MESH
// worker plist (FEATURE 26, layer a). The launchd plist splits Program (the real
// binary path launchd execs) from ProgramArguments[0] (the argv[0] the process
// shows), so `ps aux` over the live mesh shows this generic helper token instead
// of the real disguised binary path.
//
// Derived DETERMINISTICALLY from the per-role seed (the role's independent mesh
// label) rather than the salt: the daemon argv[0] is DISPLAY-ONLY (nothing reads
// it back — the mesh recovers its role from env and its binary from
// os.Executable), and it must be present from the FIRST install, before the salt
// exists. Seeding from the per-role label yields a DISTINCT token per role (so
// the three mesh procs never read as a matching set) and a STABLE token across
// every plist-generation path (install, ensure-rebuild, watchdog, self-update),
// so a recovery can never regress to a leaky argv. Empty seed ⇒ "" (caller keeps
// the legacy visible argv — dev fallback with no roster).
func DaemonArgv0(seed string) string {
	if seed == "" {
		return ""
	}
	return pickDet(procTokens, seed+"|daemon-argv0")
}

// PlatformBinBase is the deterministic disguised basename for the platform binary
// of version v: "<token>.<hex>" derived from salt+version. No 'platform' literal,
// no version string. Empty salt ⇒ "" (caller falls back to the legacy layout).
// Distinct versions get distinct bases (v is in the seed); the same version
// always maps to the same base so a re-fetch lands at the path the daemon expects.
func PlatformBinBase(salt, v string) string {
	if salt == "" {
		return ""
	}
	return pickDet(platBinWords, salt+"|platform-bin|"+v) + "." + randHexDet(salt+"|platform-binhex|"+v, 5)
}
