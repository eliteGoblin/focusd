package guard

import (
	_ "embed"
	"fmt"
	"os/exec"
	"strings"
)

// activeTabsScript is the AppleScript, compiled into the binary. No
// external .applescript file is shipped or required at runtime.
//
//go:embed scripts/active_tabs.applescript
var activeTabsScript string

// osascriptPath is the macOS automation entrypoint. A var (not const)
// so tests can point it at a missing binary to exercise the failure
// path without invoking real automation.
var osascriptPath = "/usr/bin/osascript"

// RealListTabs runs the embedded AppleScript via `osascript -` (script
// on stdin) and parses "APP\tURL" lines. macOS-only; on other OSes (or
// without Automation permission) osascript fails and the error
// propagates so the platform records a clean runtime error.
func RealListTabs() ([]Tab, error) {
	cmd := exec.Command(osascriptPath, "-")
	cmd.Stdin = strings.NewReader(activeTabsScript)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("osascript failed: %w (stderr: %s)",
			err, strings.TrimSpace(stderr.String()))
	}
	return parseTabs(stdout), nil
}

// parseTabs turns osascript's "APP\tURL\n" output into Tabs. Pure and
// unit-tested; malformed/blank lines are skipped.
func parseTabs(stdout []byte) []Tab {
	var tabs []Tab
	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		app, url, ok := strings.Cut(line, "\t")
		if !ok || strings.TrimSpace(url) == "" {
			continue
		}
		tabs = append(tabs, Tab{App: strings.TrimSpace(app), URL: strings.TrimSpace(url)})
	}
	return tabs
}

// RealKill terminates a browser: graceful AppleScript quit first (lets
// it flush state), then force-kill any survivors including renderer/GPU
// subprocesses. `pkill` exit status 1 means "nothing matched" — the
// browser is already gone, which is success for our purposes.
func RealKill(app string) error {
	_ = exec.Command(osascriptPath, "-e",
		fmt.Sprintf("tell application %q to quit", app)).Run()

	out, err := exec.Command("/usr/bin/pkill", "-i", "-f", app).CombinedOutput()
	if benign := classifyPkillErr(err); benign {
		return nil
	}
	return fmt.Errorf("pkill %s: %w (%s)", app, err, strings.TrimSpace(string(out)))
}

// classifyPkillErr reports whether a pkill error is benign. pkill exits
// 1 when no process matched — for us that means the browser is already
// gone, i.e. success. Any other error is a real failure.
func classifyPkillErr(err error) (benign bool) {
	if err == nil {
		return true
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return true
	}
	return false
}
