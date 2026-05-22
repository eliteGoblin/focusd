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
	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
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
	Interval time.Duration // reconcile / ensure interval
	// Base is the disguised, per-install random launchd label base
	// (user/system only; e.g. "com.apple.metadata.helper.7f3a2c11").
	// Empty + non-test falls back to the non-disguised default (dev).
	// Test mode always uses the fixed e2e base so e2e stays
	// deterministic/safely removable.
	Base string
}

// isTest reports whether this is the throwaway e2e install mode.
func (s Spec) isTest() bool { return s.Mode == mode.Test }

func (s Spec) base() string {
	if s.isTest() {
		return "com.focusd.daemon.e2e" // fixed → deterministic, safely removable
	}
	if s.Base != "" {
		return s.Base // user/system: per-install random disguised base
	}
	return "com.focusd.daemon" // dev fallback (unsigned, not disguised)
}

// Label is the launchd label for a role. The label scheme itself lives in
// exactly one place — relocate.RoleLabel — so the shared-prefix design
// can be reviewed/changed in a single function (see the issue referenced
// there). This method only picks the base (test/prod/dev).
func (s Spec) Label(r Role) string { return relocate.RoleLabel(s.base(), string(r)) }

// LabelFor builds a role label for the given test-mode flag.
func LabelFor(testMode bool, r Role) string {
	m := mode.User
	if testMode {
		m = mode.Test
	}
	return Spec{Mode: m}.Label(r)
}
