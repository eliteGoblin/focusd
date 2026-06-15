// Package osadapter is the OS boundary for daemon lifecycle. It manages
// the THREE launchd entries that form the self-protection mesh
// (daemon_design.md §3/§4):
//
//	role A  (KeepAlive+RunAtLoad)  reconcile platform + recreate siblings
//	role B  (KeepAlive+RunAtLoad)  identical peer
//	ensure  (StartInterval)        recreate A/B/ensure if missing, exit
//
// Each runs the SAME binary (os.Executable) with a different --r role,
// so any survivor can rebuild the others (no registry; structural).
package osadapter

import (
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// Role identifies one of the three mesh entries.
type Role string

const (
	RoleA      Role = "a"
	RoleB      Role = "b"
	RoleEnsure Role = "ensure"
)

// AllRoles is the full mesh, in install order.
var AllRoles = []Role{RoleA, RoleB, RoleEnsure}

// Spec describes how to install/recreate the daemon mesh.
type Spec struct {
	Mode     mode.Mode     // user | system | test (decided once at bootstrap)
	SelfPath string        // absolute path of the daemon binary
	Workdir  string        // daemon work directory
	Github   string        // owner/repo
	Asset    string        // release asset filename
	Interval time.Duration // worker reconcile cadence (the fast in-process self-heal, ~2s)
	// EnsureInterval is the ensurer plist's StartInterval — the periodic
	// launchd backstop (FEATURE 10 / ADR-0014). DECOUPLED from Interval:
	// the workers tick fast (~2s) for the real self-heal; the ensurer
	// stays a ~10s backstop because launchd floors small StartInterval
	// values. Empty → EnsureBackstopInterval.
	EnsureInterval time.Duration
	// Roster carries the three INDEPENDENT mesh labels (FEATURE 10 /
	// ADR-0014), positional and aligned with AllRoles (index 0 → RoleA,
	// 1 → RoleB, 2 → RoleEnsure). Generated once at install via
	// relocate.GenerateRoster (distinct vendor families, no shared base,
	// no role-revealing token) and persisted in the masked workdir roster
	// + baked into each plist's argv so any survivor reconstructs it.
	// Empty + non-test falls back to the non-disguised dev labels. Test
	// mode ignores Roster and uses the fixed e2e labels.
	Roster []string
}

// isTest reports whether this is the throwaway e2e install mode.
func (s Spec) isTest() bool { return s.Mode == mode.Test }

// Label is the launchd label for a role. Resolution order:
//   - test mode → the fixed, deterministic e2e label (safely removable)
//   - a populated Roster → the independent label at the role's position
//     in AllRoles (FEATURE 10: no shared base, used verbatim)
//   - otherwise → the non-disguised dev fallback "com.focusd.daemon.<r>"
func (s Spec) Label(r Role) string {
	if s.isTest() {
		return "com.focusd.daemon.e2e." + string(r) // fixed → deterministic
	}
	if i := roleIndex(r); i >= 0 && i < len(s.Roster) && s.Roster[i] != "" {
		return s.Roster[i] // user/system: independent per-role disguised label
	}
	return "com.focusd.daemon." + string(r) // dev fallback (unsigned, not disguised)
}

// roleIndex returns r's position in AllRoles, or -1 if not found.
func roleIndex(r Role) int {
	for i, rr := range AllRoles {
		if rr == r {
			return i
		}
	}
	return -1
}

// LabelFor builds a role label for the given test-mode flag.
func LabelFor(testMode bool, r Role) string {
	m := mode.User
	if testMode {
		m = mode.Test
	}
	return Spec{Mode: m}.Label(r)
}
