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

import "time"

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
	TestMode bool          // easily-removable test labels (safe e2e)
	SelfPath string        // absolute path of the daemon binary
	Workdir  string        // daemon work directory
	Github   string        // owner/repo
	Asset    string        // release asset filename
	Interval time.Duration // reconcile / ensure interval
}

func (s Spec) base() string {
	if s.TestMode {
		return "com.focusd.daemon.e2e"
	}
	return "com.focusd.daemon"
}

// Label is the launchd label for a role.
func (s Spec) Label(r Role) string { return s.base() + "." + string(r) }

// LabelFor builds a role label for the given test-mode flag.
func LabelFor(testMode bool, r Role) string {
	return Spec{TestMode: testMode}.Label(r)
}
