package osadapter

import (
	"os"
	"strings"
)

// MeshEnvKey is the launchd EnvironmentVariables key that carries a PROD mesh
// member's role marker (FEATURE 19). The marker moved OFF the command line —
// where `ps` exposes argv to root — and into the plist environment, which the
// process list does not display. The name is deliberately opaque and does NOT
// reference focusd or "mesh": a casual `ps aux | grep mesh` (or `grep focusd`)
// over the live process list finds nothing tied to the install.
//
// Values (round-tripped by encodeRole / decodeMeshEnv):
//   - "run:a"  → worker role A → argv: run --r a --mesh
//   - "run:b"  → worker role B → argv: run --r b --mesh
//   - "ensure" → ensurer       → argv: ensure
const MeshEnvKey = "APP_LAUNCH_CONTEXT"

// meshEnvRunPrefix tags a WORKER role value ("run:a" / "run:b"). The ensurer
// value ("ensure") deliberately lacks it: like the pre-19 `ensure` argv (which
// carried no --mesh), an ensure-only plist must NOT corroborate a real
// mesh-worker generation in DiscoverAllGenerations.
const meshEnvRunPrefix = "run:"

// encodeRole maps a role to its MeshEnvKey value. Exact inverse of
// decodeMeshEnv (round-trip unit-tested for every role).
func encodeRole(r Role) string {
	if r == RoleEnsure {
		return string(RoleEnsure) // "ensure"
	}
	return meshEnvRunPrefix + string(r) // "run:a" / "run:b"
}

// decodeMeshEnv reconstructs the legacy subcommand argv from a MeshEnvKey
// value (FEATURE 19). It is the EXACT inverse of the prod argv the daemon used
// to bake before the marker moved into the environment:
//   - "ensure"      → ["ensure"]
//   - "run:<role>"  → ["run", "--r", <role>, "--mesh"]
//   - unset/garbage → nil  (caller falls through to usage(); never a partial
//     argv that could mis-dispatch)
//
// CRITICAL: a nil return on a bad/missing value means the prod launchd start
// degrades to usage()+exit, which KeepAlive then respawns into the same
// failure. The encode/decode pair is therefore round-trip unit-tested for
// every role so a healthy install can never land here.
func decodeMeshEnv(val string) []string {
	if val == string(RoleEnsure) {
		return []string{"ensure"}
	}
	if role := strings.TrimPrefix(val, meshEnvRunPrefix); role != val && role != "" {
		return []string{"run", "--r", role, "--mesh"}
	}
	return nil
}

// ArgvFromEnv reads MeshEnvKey from the process environment and reconstructs
// the legacy subcommand argv (FEATURE 19). Returns nil when the var is unset or
// malformed. Used by the daemon entrypoint when launchd starts it with an empty
// argv (the minimized prod plist's ProgramArguments is the binary alone).
func ArgvFromEnv() []string {
	return decodeMeshEnv(os.Getenv(MeshEnvKey))
}

// isFocusdMeshWorkerPlist reports whether a plist is a focusd self-healing
// WORKER — the corroborating "this verified binary is a real mesh generation"
// signal in DiscoverAllGenerations (FEATURE 17, extended by FEATURE 19). It is
// a UNION across the fleet transition so generation cleanup keeps working while
// old and new plists coexist:
//   - NEW (FEATURE 19) plists carry the worker marker in EnvironmentVariables:
//     MeshEnvKey="run:<role>".
//   - OLD plists carry --mesh in argv (the pre-19 marker).
//
// The ensure role corroborates NEITHER (its env value is "ensure", its old argv
// has no --mesh) — preserving the pre-19 semantic that an ensure-only plist is
// not a real mesh. The Ed25519-verified argv[0] remains the PRIMARY identity
// key; this is only the corroborating signal.
func isFocusdMeshWorkerPlist(env map[string]string, argv []string) bool {
	return strings.HasPrefix(env[MeshEnvKey], meshEnvRunPrefix) || hasMeshFlag(argv)
}
