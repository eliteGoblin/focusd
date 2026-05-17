// Package osadapter is the OS boundary for daemon lifecycle (launchd on
// macOS). Core daemon logic never references launchd directly.
package osadapter

import "time"

// Spec describes how to install the daemon as an OS service.
type Spec struct {
	TestMode bool          // use the easily-removable test label/dir
	SelfPath string        // absolute path of the daemon binary
	Workdir  string        // daemon work directory
	Github   string        // owner/repo
	Asset    string        // release asset filename
	Interval time.Duration // reconcile interval
}

// Label is the launchd label (distinct in test mode so e2e is safely
// removable and never collides with a real install).
func (s Spec) Label() string {
	if s.TestMode {
		return "com.focusd.daemon.e2e"
	}
	return "com.focusd.daemon"
}

// LabelFor returns the label for the given test-mode flag.
func LabelFor(testMode bool) string { return Spec{TestMode: testMode}.Label() }
