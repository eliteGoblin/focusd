package infra

import (
	"strings"
	"testing"
)

// newPlistManager returns a manager wired for the requested mode. The dir
// and path fields don't matter for content generation tests.
func newPlistManager(mode ExecMode) *LaunchdManagerImpl {
	return &LaunchdManagerImpl{
		mode:      mode,
		plistDir:  "/tmp",
		plistPath: "/tmp/test.plist",
	}
}

func TestGeneratePlistContent_UserMode_HasCronLikeKeys(t *testing.T) {
	m := newPlistManager(ExecModeUser)
	out, err := m.generatePlistContent("/Users/test/.local/bin/appmon")
	if err != nil {
		t.Fatalf("generatePlistContent: %v", err)
	}
	body := string(out)

	mustContain(t, body, "<key>RunAtLoad</key>")
	mustContain(t, body, "<key>StartInterval</key>")
	mustContain(t, body, "<integer>300</integer>")

	mustContain(t, body, "<key>KeepAlive</key>")
	mustContain(t, body, "<key>Crashed</key>")
	mustContain(t, body, "<key>SuccessfulExit</key>")
	mustContain(t, body, "<false/>")

	mustContain(t, body, "<key>ThrottleInterval</key>")
	mustContain(t, body, "<integer>10</integer>")

	mustContain(t, body, "<string>/Users/test/.local/bin/appmon</string>")
	mustContain(t, body, "<string>start</string>")
}

func TestGeneratePlistContent_SystemMode_HasCronLikeKeys(t *testing.T) {
	m := newPlistManager(ExecModeSystem)
	out, err := m.generatePlistContent("/usr/local/bin/appmon")
	if err != nil {
		t.Fatalf("generatePlistContent: %v", err)
	}
	body := string(out)

	mustContain(t, body, "<key>RunAtLoad</key>")
	mustContain(t, body, "<key>StartInterval</key>")
	mustContain(t, body, "<integer>300</integer>")
	mustContain(t, body, "<key>KeepAlive</key>")
	mustContain(t, body, "<key>Crashed</key>")
	mustContain(t, body, "<key>SuccessfulExit</key>")
	mustContain(t, body, "<false/>")
	mustContain(t, body, "<string>/usr/local/bin/appmon</string>")
}

// Regression: must not fall back to the old "KeepAlive: true" form, which
// would cause launchd to loop the cleanly-exiting `start` parent every
// ThrottleInterval (10s) — wasteful and noisy.
func TestGeneratePlistContent_SystemMode_DoesNotUseBareKeepAlive(t *testing.T) {
	m := newPlistManager(ExecModeSystem)
	out, _ := m.generatePlistContent("/usr/local/bin/appmon")
	body := string(out)

	// Old form was "<key>KeepAlive</key>\n    <true/>" — the dict form is
	// what we want now. Ensure no <key>KeepAlive</key> immediately followed
	// by <true/>.
	idx := strings.Index(body, "<key>KeepAlive</key>")
	if idx < 0 {
		t.Fatalf("KeepAlive key missing")
	}
	tail := body[idx:]
	dictPos := strings.Index(tail, "<dict>")
	truePos := strings.Index(tail, "<true/>")
	if truePos >= 0 && (dictPos < 0 || truePos < dictPos) {
		t.Fatalf("KeepAlive resolves to <true/> before <dict> — regression of cron config")
	}
}

func mustContain(t *testing.T, body, needle string) {
	t.Helper()
	if !strings.Contains(body, needle) {
		t.Fatalf("plist body missing %q\nbody:\n%s", needle, body)
	}
}
