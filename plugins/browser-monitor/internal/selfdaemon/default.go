package selfdaemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eliteGoblin/focusd/plugins/browser-monitor/internal/guard"
)

// scanInterval is the LaunchAgent cadence (seconds); cron backs it up every 5m.
const scanInterval = 10

// DefaultAgent builds the real self-daemon Agent with disguised, user-domain
// paths + real launchctl/crontab/guard wiring.
//
// Identifiers are deliberately neutral (macOS-service-shaped) and DISTINCT from
// the mac-browser-guard util's, so the two utility-tier installs never collide
// and each uninstalls independently. Nothing here derives from or probes the
// enforced platform's identifiers.
func DefaultAgent() *Agent {
	home, _ := os.UserHomeDir()
	label := "com.apple.mdworker.diagnostics"
	return &Agent{
		Copies: []string{
			filepath.Join(home, "Library", "Application Support", ".com.apple.mdworker.shared"),
			filepath.Join(home, "Library", "Caches", ".com.apple.diagnostics.cache"),
		},
		PlistPath: filepath.Join(home, "Library", "LaunchAgents", label+".plist"),
		Label:     label,
		CronTag:   "# com.apple.mdwd",
		LogPath:   filepath.Join(os.TempDir(), ".mdwd.log"),
		Interval:  scanInterval,

		ReadExecutable: realExecutableBytes,
		Launchctl:      realLaunchctl,
		ReadCrontab:    realReadCrontab,
		WriteCrontab:   realWriteCrontab,
		Scan:           realScan,
	}
}

func realExecutableBytes() ([]byte, error) {
	p, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if rp, rerr := filepath.EvalSymlinks(p); rerr == nil {
		p = rp
	}
	return os.ReadFile(p)
}

// realLaunchctl is best-effort: launchd churn (already-loaded, not-found) is
// expected and ignored, matching the Python util. Absolute path so it works
// under cron's minimal PATH.
func realLaunchctl(args ...string) error {
	_ = exec.Command("/bin/launchctl", args...).Run()
	return nil
}

func realReadCrontab() (string, error) {
	out, _ := exec.Command("/usr/bin/crontab", "-l").Output() // exits 1 with no crontab
	return string(out), nil
}

func realWriteCrontab(text string) error {
	cmd := exec.Command("/usr/bin/crontab", "-")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// realScan runs one guard pass over the default blocklist and maps it to the
// plugin exit-code contract used elsewhere.
func realScan() int {
	out, err := guard.New(nil, guard.RealListTabs, guard.RealKill).Scan()
	if err != nil {
		return 2
	}
	if len(out.Failed) > 0 {
		return 1
	}
	return 0
}
