// Package main is the CLI entry point for appmon.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/eliteGoblin/focusd/app_mon/internal/daemon"
	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
	"github.com/eliteGoblin/focusd/app_mon/internal/infra"
	"github.com/eliteGoblin/focusd/app_mon/internal/policy"
	"github.com/eliteGoblin/focusd/app_mon/internal/usecase"

	// Register SQLCipher driver
	_ "github.com/mutecomm/go-sqlcipher/v4"
)

// daemonSelfRelocate re-execs the current daemon process from a relocated
// binary path if its executable still lives outside the relocator cache.
// This guards against spawners that bypass the relocator (e.g., an older
// CLI driving an update, or a manual `appmon daemon` invocation from
// /usr/local/bin/appmon). Idempotent: if argv[0] is already under the
// relocator dir, returns nil without changing anything.
//
// On successful re-exec the function does not return — syscall.Exec
// replaces the process image. On any failure the caller continues with
// the original (un-relocated) executable; the daemon still functions,
// just with `p_comm = appmon` which `killall appmon` would match.
func daemonSelfRelocate() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	relocator := infra.NewRelocator(infra.GetRealUserHome())
	if strings.HasPrefix(exe, relocator.Dir()+string(os.PathSeparator)) {
		return nil // already relocated
	}
	relocated, err := relocator.Relocate(exe)
	if err != nil {
		return err
	}
	argv := append([]string{relocated}, os.Args[1:]...)
	return syscall.Exec(relocated, argv, os.Environ())
}

var (
	// Version info (set via ldflags during release build)
	// go build -ldflags "-X main.Version=x.y.z -X main.Commit=abc123 -X main.BuildTime=2024-01-01"
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "appmon",
	Short: "Application monitor - blocks distracting apps",
	Long: `appmon is a daemon that monitors and removes distracting applications
like Steam and Dota 2. It runs in the background and automatically
kills blocked processes and deletes their files.

For your own good, there is no stop command.`,
	Version: Version,
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start protection (launches watcher and guardian daemons)",
	Long: `Starts both the watcher and guardian daemons.
The watcher enforces blocking policies (kills processes, deletes files).
The guardian monitors the watcher and restarts it if killed.
They monitor each other for resilience.

This also installs a LaunchAgent to auto-start on login.`,
	RunE: runStart,
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check protection status",
	Long:  `Shows whether the daemons are running and what apps are being blocked.`,
	RunE:  runStatus,
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List blocked applications",
	Long:  `Shows all applications that are being blocked, including their process names and paths.`,
	RunE:  runList,
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Run enforcement scan immediately",
	Long: `Runs a one-time enforcement scan immediately without waiting for the next scheduled scan.
This kills blocked processes, uninstalls via package managers (brew, etc.), and deletes blocked paths.`,
	RunE: runScan,
}

var blocklistCmd = &cobra.Command{
	Use:   "blocklist",
	Short: "Show the DNS blocklist managed in /etc/hosts",
	Long: `Prints two lists:
  - Compiled: domains hardcoded in this binary (the permanent block set).
  - Active:   domains currently installed in /etc/hosts between the
              appmon-managed markers.

If Compiled and Active diverge, the watcher will reconcile within ~60s.
The blocklist is enforced in system mode only; user-mode daemons can't
write /etc/hosts.`,
	RunE: runBlocklist,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Prints version, commit, and build time. Use --json for machine-readable output.`,
	Run:   runVersion,
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update appmon to the latest version",
	Long: `Downloads and installs the latest version of appmon from GitHub releases.
The update process includes:
1. Check for newer version
2. Download new binary
3. Stop running daemons
4. Replace binary and update backups
5. Restart daemons
6. Verify daemons are healthy

If daemons fail to start after update, automatically rolls back to previous version.`,
	RunE: runUpdate,
}

// Hidden daemon command - used for self-exec when spawning daemons
var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Hidden: true,
	RunE:   runDaemon,
}

var (
	daemonRole      string
	daemonName      string
	daemonMode      string // mode passed to daemon subprocess
	jsonOutput      bool
	checkOnly       bool
	modeFlag        string // --mode user|system override for start command
	localBinaryPath string // --local-binary for testing updates with local binary
)

func init() {
	daemonCmd.Flags().StringVar(&daemonRole, "role", "", "Daemon role (watcher/guardian)")
	daemonCmd.Flags().StringVar(&daemonName, "name", "", "Obfuscated process name")
	daemonCmd.Flags().StringVar(&daemonMode, "mode", "", "Execution mode (user/system)")
	versionCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output version info as JSON")
	updateCmd.Flags().BoolVar(&checkOnly, "check", false, "Only check for updates, don't install")
	updateCmd.Flags().StringVar(&localBinaryPath, "local-binary", "", "Path to local binary for testing (skips GitHub download)")
	startCmd.Flags().StringVar(&modeFlag, "mode", "", "Override execution mode (user|system)")

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(scanCmd)
	rootCmd.AddCommand(blocklistCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(daemonCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	// Detect execution mode (sudo vs non-sudo)
	execMode := infra.DetectExecMode()

	// Handle --mode flag override
	if modeFlag != "" {
		switch modeFlag {
		case "user":
			if execMode.IsRoot {
				// Running as root but want user mode
				// This is a cleanup operation - stop system mode and let user restart in user mode
				fmt.Println("Cleaning up system mode to switch to user mode...")
				pm := infra.NewProcessManager()

				// Kill all appmon daemons
				pids, _ := pm.FindByName("appmon")
				for _, pid := range pids {
					if pid != pm.GetCurrentPID() {
						_ = pm.Kill(pid)
					}
				}

				// Remove system plist(s) - both old static and randomized
				oldSystemPlist := "/Library/LaunchDaemons/" + infra.DefaultLaunchdLabel + ".plist"
				if _, err := os.Stat(oldSystemPlist); err == nil {
					_ = exec.Command("launchctl", "unload", oldSystemPlist).Run()
					_ = os.Remove(oldSystemPlist)
					fmt.Println("Removed old system LaunchDaemon")
				}

				// Clear encrypted registry and remove randomized system plist
				systemExecMode := infra.DetectExecMode()
				if cleanupReg, cleanupErr := openEncryptedRegistry(systemExecMode, pm); cleanupErr == nil {
					if label, labelErr := infra.EnsurePlistLabel(cleanupReg); labelErr == nil && label != infra.DefaultLaunchdLabel {
						randomizedPlist := "/Library/LaunchDaemons/" + label + ".plist"
						if _, err := os.Stat(randomizedPlist); err == nil {
							_ = exec.Command("launchctl", "unload", randomizedPlist).Run()
							_ = os.Remove(randomizedPlist)
							fmt.Println("Removed system LaunchDaemon")
						}
					}
					_ = cleanupReg.Clear()
					cleanupReg.Close()
				}

				fmt.Println("\nSystem mode cleaned up successfully.")
				fmt.Println("Now run without sudo to start user mode:")
				fmt.Println("  ./appmon start")
				return nil
			}
			// Already running as user, just use default detection
		case "system":
			if !execMode.IsRoot {
				fmt.Println("Error: --mode system requires sudo")
				fmt.Println("Run: sudo ./appmon start --mode system")
				return fmt.Errorf("system mode requires root privileges")
			}
			// Already root, use system mode (default)
		default:
			return fmt.Errorf("invalid --mode value: %s (use 'user' or 'system')", modeFlag)
		}
	}

	fmt.Printf("Execution mode: %s\n", execMode.Mode)
	if execMode.Mode == infra.ExecModeSystem {
		fmt.Println("Running as root - will install as LaunchDaemon (system-wide)")
	} else {
		fmt.Println("Running as user - will install as LaunchAgent (user-space)")
	}

	// Initialize process manager early for cross-mode detection
	pm := infra.NewProcessManager()

	// Cross-mode daemon detection: check if daemons are running in the OTHER mode
	// This handles the case where user runs "sudo appmon start" while user mode is running,
	// or runs "appmon start" while system mode is running.
	if err := detectAndCleanupOtherModeDaemons(execMode, pm); err != nil {
		return fmt.Errorf("failed to cleanup other mode daemons: %w", err)
	}

	// Continue with normal initialization
	registry, err := openEncryptedRegistry(execMode, pm)
	if err != nil {
		return fmt.Errorf("failed to initialize registry: %w", err)
	}
	defer registry.Close()

	// Ensure randomized plist label exists (generated on first install, persisted)
	plistLabel, err := infra.EnsurePlistLabel(registry)
	if err != nil {
		return fmt.Errorf("failed to ensure plist label: %w", err)
	}

	// Rebuild ExecModeConfig with the randomized plist label
	execMode = rebuildExecModeWithLabel(execMode, plistLabel)

	// Check if system mode is running (for mode switch detection)
	// Check both old static plist and current randomized label
	systemPlistExists := fileExists("/Library/LaunchDaemons/"+infra.DefaultLaunchdLabel+".plist") ||
		fileExists("/Library/LaunchDaemons/"+plistLabel+".plist")

	// Check if already running - handle mode switching.
	//
	// Liveness here means BOTH the kernel sees the PID AND the registry's
	// last_heartbeat is fresh. Stale-heartbeat with a live PID is the
	// stuck-daemon case (deadlock, hung syscall); we treat it as dead so
	// the spawn path below tears it down and respawns. This is what makes
	// `sudo appmon start` a universal recovery button.
	entry, _ := registry.GetAll()
	if entry != nil {
		watcherAlive := pm.IsRunning(entry.WatcherPID) && heartbeatFresh(entry.LastHeartbeat)
		guardianAlive := pm.IsRunning(entry.GuardianPID) && heartbeatFresh(entry.LastHeartbeat)

		if watcherAlive && guardianAlive {
			// Check if mode matches
			currentMode := string(execMode.Mode)
			if entry.Mode == currentMode || entry.Mode == "" {
				// Same mode - compare versions before deciding
				installedVersion := getInstalledVersion(execMode.BinaryPath)

				if isNewerVersion(Version, installedVersion) {
					// Current binary is newer - upgrade
					fmt.Printf("Upgrading from %s to %s...\n", installedVersion, Version)
					_ = pm.Kill(entry.WatcherPID)
					_ = pm.Kill(entry.GuardianPID)
					_ = registry.Clear()
					time.Sleep(1 * time.Second)
					// Continue to install and restart
				} else if Version == installedVersion {
					// Same version - already up to date
					fmt.Println("appmon is already running (fully protected)")
					return nil
				} else {
					// Installed is newer - don't downgrade
					fmt.Printf("Already running newer version %s (not downgrading from %s)\n",
						installedVersion, Version)
					return nil
				}
			}
			// Mode switch requested - kill old daemons and proceed
			fmt.Printf("Switching from %s to %s mode...\n", entry.Mode, currentMode)
			_ = pm.Kill(entry.WatcherPID)
			_ = pm.Kill(entry.GuardianPID)
			_ = registry.Clear()
			time.Sleep(1 * time.Second) // Wait for processes to die
		}
	} else if systemPlistExists && execMode.Mode == infra.ExecModeUser {
		// Registry not readable but system plist exists - system mode is running
		if !execMode.IsRoot {
			// User mode without sudo - can't switch from system mode
			fmt.Println("Error: appmon is running in system mode.")
			fmt.Println("To switch to user mode, run: sudo ./appmon start --mode user")
			return nil
		}
		// Running with sudo and --mode user - kill system daemons and clear registry
		fmt.Println("Switching from system to user mode...")
		// Kill any running system daemons by finding them
		pids, _ := pm.FindByName("appmon")
		for _, pid := range pids {
			if pid != pm.GetCurrentPID() {
				_ = pm.Kill(pid)
			}
		}
		// Clear registry (has root ownership from system mode)
		_ = registry.Clear()
		time.Sleep(1 * time.Second)
	}

	// Pre-flight cleanup. Reaches us when we've decided to (re)spawn — kill
	// any leftover daemon processes so we don't pile up multiple watchers /
	// guardians from prior failed sessions:
	//   - "appmon"-named processes with `daemon --role` argv → legacy
	//     v0.5.0 ghosts that don't run through the relocator
	//   - cache-dir processes (running under the obfuscated basename) →
	//     stale relocated daemons whose registry entries timed out
	// Idempotent: empty when state is clean. Exempts our own CLI PID.
	cleanupStaleDaemonProcesses(pm)

	// Determine binary path based on execution mode
	var binaryPath string
	currentExecPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Copy binary to appropriate location if not already there
	binaryPath = execMode.BinaryPath
	if currentExecPath != binaryPath {
		// Ensure directory exists
		if err := os.MkdirAll(filepath.Dir(binaryPath), 0755); err != nil {
			fmt.Printf("Warning: Could not create binary directory: %v\n", err)
			binaryPath = currentExecPath // Fall back to current location
		} else {
			// Copy binary to destination
			if err := copyBinary(currentExecPath, binaryPath); err != nil {
				fmt.Printf("Warning: Could not copy binary to %s: %v\n", binaryPath, err)
				binaryPath = currentExecPath // Fall back to current location
			} else {
				fmt.Printf("Installed binary to %s\n", binaryPath)
			}
		}
	}

	// Set up binary backups for self-protection (with hybrid backup)
	backupManager := infra.NewBackupManager()
	if err := backupManager.SetupBackupsWithMode(binaryPath, Version, BuildTime, execMode.Mode); err != nil {
		fmt.Printf("Warning: Could not setup binary backups: %v\n", err)
		fmt.Println("         (appmon will run, but binary won't be auto-restored if deleted)")
	} else {
		fmt.Printf("Binary backups created (v%s) with GitHub fallback\n", Version)
	}

	// Build the launch stub the plist will reference. Using a randomized
	// system-looking basename keeps "appmon" out of Login Items (the UI
	// shows the basename of ProgramArguments[0]).
	plistExec := binaryPath
	if stub, err := infra.EnsureLaunchStub(binaryPath, infra.GetRealUserHome(), registry); err == nil {
		plistExec = stub
	} else {
		fmt.Printf("Warning: Could not create launch stub: %v\n", err)
		fmt.Println("         (auto-start will still work, but Login Items will show 'appmon')")
	}

	// Install LaunchAgent/LaunchDaemon based on execution mode (idempotent)
	launchdManager := infra.NewLaunchdManager(execMode)

	// Cleanup plist from wrong mode (handles user↔system migration)
	if err := launchdManager.CleanupOtherMode(); err != nil {
		fmt.Printf("Warning: Could not cleanup other mode plist: %v\n", err)
	}

	// Cleanup old static plist from pre-v0.5.0 (com.focusd.appmon)
	cleanupOldStaticPlist(execMode)

	if !launchdManager.IsInstalled() {
		// Fresh install
		if err := launchdManager.Install(plistExec); err != nil {
			fmt.Printf("Warning: Could not install %s: %v\n", execMode.Mode, err)
			fmt.Println("         (appmon will still run, but won't auto-start)")
		} else {
			if execMode.IsRoot {
				fmt.Println("Installed LaunchDaemon for system-wide auto-start")
			} else {
				fmt.Println("Installed LaunchAgent for auto-start on login")
			}
		}
	} else if launchdManager.NeedsUpdate(plistExec) {
		// Exists but outdated - update in place
		if err := launchdManager.Update(plistExec); err != nil {
			fmt.Printf("Warning: Could not update %s: %v\n", execMode.Mode, err)
		} else {
			fmt.Println("Updated plist with new binary path")
		}
	} else {
		// Already installed with correct config - skip
		fmt.Println("Plist already installed and up-to-date")
	}

	// Start both daemons with the determined mode
	if err := daemon.StartBothDaemonsWithMode(string(execMode.Mode)); err != nil {
		return fmt.Errorf("failed to start daemons: %w", err)
	}

	// Wait a moment for daemons to register
	time.Sleep(500 * time.Millisecond)

	fmt.Println("\n=== appmon Started ===")
	fmt.Printf("Mode: %s\n", execMode.Mode)
	fmt.Printf("Binary: %s\n", binaryPath)
	fmt.Println("Status: PROTECTED")
	fmt.Println("\nBlocked applications:")

	policyRegistry := policy.NewRegistry()
	for _, p := range policyRegistry.GetAll() {
		fmt.Printf("  - %s\n", p.Name())
	}

	fmt.Println("\nDaemons are running in the background.")
	fmt.Println("They will restart automatically if killed.")
	if execMode.IsRoot {
		fmt.Println("System features available: hosts file, firewall (future)")
	}
	fmt.Println("=====================")

	return nil
}

// copyBinary copies the binary file to destination using atomic write pattern.
// Writes to temp file first, syncs, chmods, then renames to avoid corruption.
func copyBinary(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// Create temp file in same directory for atomic rename
	dstDir := filepath.Dir(dst)
	tmpFile, err := os.CreateTemp(dstDir, ".appmon-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	// Clean up temp file on any error
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	// Copy content
	if _, err = io.Copy(tmpFile, sourceFile); err != nil {
		tmpFile.Close()
		return err
	}

	// Sync to disk before rename
	if err = tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	// Make executable before rename
	if err = os.Chmod(tmpPath, 0755); err != nil {
		return err
	}

	// Atomic rename
	if err = os.Rename(tmpPath, dst); err != nil {
		return err
	}

	success = true
	return nil
}

func runStatus(cmd *cobra.Command, args []string) error {
	pm := infra.NewProcessManager()

	fmt.Println("\n=== appmon Status ===")

	// Source of truth for "is appmon running": ps. The encrypted registry
	// is mode-scoped (user vs system), so a user-mode CLI cannot read the
	// system-mode registry — it'd report NOT RUNNING even when system-
	// mode daemons are alive. ps is world-readable, so we use it for
	// liveness and treat the registry as enrichment only.
	live, _ := infra.DetectLiveDaemons()
	hasWatcher, hasGuardian := false, false
	for _, d := range live {
		if d.Role == "watcher" {
			hasWatcher = true
		}
		if d.Role == "guardian" {
			hasGuardian = true
		}
	}
	switch {
	case hasWatcher && hasGuardian:
		fmt.Println("Status: RUNNING (fully protected)")
	case hasWatcher || hasGuardian:
		fmt.Println("Status: DEGRADED (partial protection)")
		if !hasWatcher {
			fmt.Println("        Watcher is down (will be restarted by guardian)")
		}
		if !hasGuardian {
			fmt.Println("        Guardian is down (will be restarted by watcher)")
		}
	default:
		fmt.Println("Status: NOT RUNNING")
	}

	execMode := infra.DetectExecMode()

	// Read-only registry probe: don't auto-create the key/DB just to
	// answer "what's the status?" — that would silently recreate the
	// user-mode footprint we just cleaned up, putting us back in the
	// dual-mode trap. If no key exists for this mode's data dir, we
	// fall through to ps-only output.
	keyProvider := infra.NewFileKeyProvider(execMode.DataDir)
	if !keyProvider.KeyExists() {
		if hasWatcher || hasGuardian {
			fmt.Println("\nNote: daemons run in a different mode than this CLI.")
			fmt.Println("      For full version + heartbeat details, run:")
			fmt.Println("        sudo /usr/local/bin/appmon status")
		} else {
			fmt.Println("\nRun 'appmon start' (or 'sudo appmon start' for system mode) to enable protection.")
		}
		printLiveDaemonList(live)
		return nil
	}

	registry, regErr := openEncryptedRegistry(execMode, pm)
	if regErr != nil {
		// Registry exists but unreadable (e.g. permission). ps liveness
		// is already printed above; just hint at the right command.
		if hasWatcher || hasGuardian {
			fmt.Println("\nNote: cannot read this mode's registry (permission?).")
			fmt.Println("      For full version + heartbeat details, run:")
			fmt.Println("        sudo /usr/local/bin/appmon status")
		} else {
			fmt.Println("\nRun 'appmon start' to enable protection.")
		}
		printLiveDaemonList(live)
		return nil
	}
	defer registry.Close()

	if plistLabel, labelErr := infra.EnsurePlistLabel(registry); labelErr == nil {
		_ = rebuildExecModeWithLabel(execMode, plistLabel)
	}

	backupManager := infra.NewBackupManager()

	entry, err := registry.GetAll()
	if err != nil || entry == nil {
		if hasWatcher || hasGuardian {
			fmt.Println("\nNote: daemons running but this CLI's registry is empty.")
			fmt.Println("      Likely a mode mismatch; try: sudo /usr/local/bin/appmon status")
		} else {
			fmt.Println("\nRun 'appmon start' to enable protection.")
		}
		printLiveDaemonList(live)
		return nil
	}

	// If ps says daemons are alive but the registry shows different PIDs
	// (or empty), flag it — usually means the registry is stale or from
	// the other mode.
	registryAgreesWithPS := false
	if hasWatcher && pm.IsRunning(entry.WatcherPID) &&
		hasGuardian && pm.IsRunning(entry.GuardianPID) {
		registryAgreesWithPS = true
	}
	if (hasWatcher || hasGuardian) && !registryAgreesWithPS {
		fmt.Println("        (registry shows stale PIDs — likely a mode mismatch;")
		fmt.Println("         live daemons listed below come from ps, not the registry)")
	}

	// Show version info
	fmt.Printf("\nCLI version:    %s\n", Version)
	if entry.AppVersion != "" {
		fmt.Printf("Daemon version: %s\n", entry.AppVersion)
		if entry.AppVersion != Version {
			fmt.Println("                (differs from CLI - consider restarting)")
		}
	}

	// Check backup config for execution mode
	backupConfig, err := backupManager.GetConfig()
	if err == nil {
		modeStr := backupConfig.ExecMode
		if modeStr == "" {
			modeStr = "user"
		}
		fmt.Printf("Execution mode: %s\n", modeStr)

		// Auto-start: ask the same component that installs the plist
		// whether it's installed. Earlier versions os.Stat'd the path
		// stored in the backup config, which drifted whenever the
		// randomized plist label rotated (label is regenerated on
		// EnsurePlistLabel's first call per session), leaving status
		// showing "disabled" while the plist was actually loaded. The
		// LaunchdManager uses the current label and mode, so it stays
		// in sync with reality.
		statusExecMode := infra.DetectExecMode()
		if backupConfig.ExecMode == string(infra.ExecModeSystem) {
			// Force-resolve system-mode paths if the backup said so,
			// even though we may be invoked as a non-root user.
			statusExecMode.Mode = infra.ExecModeSystem
			statusExecMode.PlistDir = "/Library/LaunchDaemons"
		}
		if plistLabel := infra.GetLaunchdLabel(); plistLabel != "" {
			statusExecMode.PlistPath = filepath.Join(statusExecMode.PlistDir, plistLabel+".plist")
		}
		ldm := infra.NewLaunchdManager(statusExecMode)
		if ldm.IsInstalled() {
			fmt.Println("Auto-start:     enabled")
		} else {
			fmt.Println("Auto-start:     disabled")
		}
	}

	// Last heartbeat
	if entry.LastHeartbeat > 0 {
		lastBeat := time.Unix(entry.LastHeartbeat, 0)
		fmt.Printf("Last heartbeat: %s ago\n", time.Since(lastBeat).Round(time.Second))
	}

	// Live processes from ps — independent of registry state, makes
	// mode-mismatch and stale-registry situations debuggable at a glance.
	printLiveDaemonList(live)

	// Blocked apps
	fmt.Println("\nBlocked applications:")
	policyRegistry := policy.NewRegistry()
	for _, p := range policyRegistry.GetAll() {
		fmt.Printf("  - %s\n", p.Name())
	}

	// Freedom protection status
	fmt.Println("\nFreedom protection:")
	freedomProtector := infra.NewFreedomProtector(pm, zap.NewNop())
	health := freedomProtector.GetHealth()
	if !health.Installed {
		fmt.Println("  - Not installed (skipped)")
	} else {
		// Build status line based on what we can protect
		// Note: FreedomProxy only runs during active blocking sessions, so we don't report it as an issue
		if health.AppRunning && health.LoginItemPresent {
			proxyStatus := ""
			if health.ProxyRunning {
				proxyStatus = ", proxy active"
			}
			if health.HelperRunning {
				fmt.Printf("  ✓ Freedom.app running%s, login item present\n", proxyStatus)
			} else {
				fmt.Printf("  ⚠ Freedom.app running%s, but helper missing (reinstall Freedom to fix)\n", proxyStatus)
			}
		} else {
			var issues []string
			if !health.AppRunning {
				issues = append(issues, "app not running")
			}
			if !health.LoginItemPresent {
				issues = append(issues, "login item missing")
			}
			fmt.Printf("  ⚠ Degraded: %s (will auto-fix)\n", strings.Join(issues, ", "))
		}
	}

	fmt.Println("=====================")
	return nil
}

func runList(cmd *cobra.Command, args []string) error {
	policyRegistry := policy.NewRegistry()

	fmt.Println("\n=== Blocked Applications ===")

	for _, p := range policyRegistry.GetAll() {
		fmt.Printf("\n[%s] %s\n", p.ID(), p.Name())
		fmt.Println("  Processes:")
		for _, proc := range p.ProcessPatterns() {
			fmt.Printf("    - %s\n", proc)
		}
		fmt.Println("  Paths:")
		for _, path := range p.PathsToDelete() {
			fmt.Printf("    - %s\n", path)
		}
		fmt.Printf("  Scan interval: %s\n", p.ScanInterval())
	}

	fmt.Println("\n============================")
	return nil
}

func runScan(cmd *cobra.Command, args []string) error {
	fmt.Println("\n=== Running Enforcement Scan ===")

	// Initialize components
	logger, _ := zap.NewDevelopment()
	defer func() { _ = logger.Sync() }()

	pm := infra.NewProcessManager()
	fs := infra.NewFileSystemManager()
	policyStore := policy.NewPolicyStore()
	strategyManager := infra.NewStrategyManager()

	// Create enforcer with strategy manager
	enforcer := usecase.NewEnforcerWithStrategy(pm, fs, policyStore, strategyManager, logger)

	// Run enforcement
	ctx := context.Background()
	results, err := enforcer.Enforce(ctx)
	if err != nil {
		return fmt.Errorf("enforcement failed: %w", err)
	}

	// Display results
	var totalKilled, totalDeleted, totalSkipped int
	var allSkippedPaths []string

	for _, r := range results {
		totalKilled += len(r.KilledPIDs)
		totalDeleted += len(r.DeletedPaths)
		totalSkipped += len(r.SkippedPaths)
		allSkippedPaths = append(allSkippedPaths, r.SkippedPaths...)

		if len(r.KilledPIDs) > 0 || len(r.DeletedPaths) > 0 {
			fmt.Printf("\n[%s]\n", r.PolicyID)
			if len(r.KilledPIDs) > 0 {
				fmt.Printf("  Killed %d processes\n", len(r.KilledPIDs))
			}
			if len(r.DeletedPaths) > 0 {
				fmt.Printf("  Deleted %d paths\n", len(r.DeletedPaths))
				for _, path := range r.DeletedPaths {
					fmt.Printf("    - %s\n", path)
				}
			}
		}
	}

	if totalKilled == 0 && totalDeleted == 0 && totalSkipped == 0 {
		fmt.Println("\nNo blocked applications found.")
	} else {
		fmt.Printf("\nTotal: %d processes killed, %d paths deleted\n", totalKilled, totalDeleted)
	}

	// Show skipped paths that need root
	if totalSkipped > 0 {
		fmt.Printf("\nSkipped (need root): %d paths\n", totalSkipped)
		for _, path := range allSkippedPaths {
			fmt.Printf("  - %s\n", path)
		}
		fmt.Println("\nRun with sudo for full cleanup: sudo ./appmon scan")
	}

	fmt.Println("================================")
	return nil
}

func runDaemon(cmd *cobra.Command, args []string) error {
	if daemonRole == "" || daemonName == "" {
		return fmt.Errorf("--role and --name are required")
	}

	// Ensure we're running from a relocated path (kernel-visible p_comm is
	// something like com.apple.cfprefsd.xpc.<hex>, not "appmon") so
	// `killall appmon` cannot match this process. Best-effort: failures
	// are non-fatal and only mean reduced obfuscation.
	if err := daemonSelfRelocate(); err != nil {
		// Log via stderr only — logger isn't initialized yet.
		fmt.Fprintf(os.Stderr, "appmon daemon: self-relocate failed: %v\n", err)
	}

	// Set up logger (writes to /var/tmp/appmon.log)
	logger := createLogger()
	defer func() { _ = logger.Sync() }()

	// Set process name for obfuscation
	daemon.SetProcessName(daemonName)

	// Create daemon entity
	role := domain.DaemonRole(daemonRole)
	d := domain.Daemon{
		PID:            os.Getpid(),
		Role:           role,
		ObfuscatedName: daemonName,
		StartedAt:      time.Now(),
		AppVersion:     Version,
	}

	// Initialize infrastructure
	pm := infra.NewProcessManager()
	fs := infra.NewFileSystemManager()

	// Determine execution mode - use explicit flag if provided, otherwise auto-detect
	var execMode *infra.ExecModeConfig
	if daemonMode == "user" {
		execMode = infra.GetUserModeConfig()
	} else if daemonMode == "system" {
		execMode = infra.DetectExecMode() // Will return system if running as root
	} else {
		execMode = infra.DetectExecMode() // Auto-detect: root → LaunchDaemon, user → LaunchAgent
	}
	registry, err := openEncryptedRegistry(execMode, pm)
	if err != nil {
		return fmt.Errorf("failed to initialize registry: %w", err)
	}
	defer registry.Close()

	// Load randomized plist label for daemon
	if plistLabel, labelErr := infra.EnsurePlistLabel(registry); labelErr == nil {
		execMode = rebuildExecModeWithLabel(execMode, plistLabel)
	}

	launchdManager := infra.NewLaunchdManager(execMode)
	policyStore := policy.NewPolicyStore()
	strategyManager := infra.NewStrategyManager()
	enforcer := usecase.NewEnforcerWithStrategy(pm, fs, policyStore, strategyManager, logger)

	// Set up graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Info("received shutdown signal")
		cancel()
	}()

	// Initialize backup manager for self-protection
	backupManager := infra.NewBackupManager()

	// Initialize Freedom protector (best effort - nil-safe if not installed)
	freedomProtector := infra.NewFreedomProtector(pm, logger)

	// Initialize hosts-file manager. The watcher owns the appmon section
	// of /etc/hosts so blocked domains can't be resolved by any process.
	// In user mode the daemon will hit EACCES on write — that's logged
	// at Debug and ignored; DNS-layer blocking is system-mode-only.
	hostsManager := infra.NewHostsManager()

	// Run appropriate daemon
	switch role {
	case domain.RoleWatcher:
		watcher := daemon.NewWatcher(
			daemon.DefaultWatcherConfig(),
			enforcer,
			registry,
			pm,
			launchdManager,
			backupManager,
			freedomProtector,
			hostsManager,
			d,
			logger,
		)
		return watcher.Run(ctx)

	case domain.RoleGuardian:
		guardian := daemon.NewGuardian(
			daemon.DefaultGuardianConfig(),
			registry,
			pm,
			d,
			logger,
		)
		return guardian.Run(ctx)

	default:
		return fmt.Errorf("unknown role: %s", role)
	}
}

func createLogger() *zap.Logger {
	config := zap.NewProductionConfig()
	config.OutputPaths = []string{"/var/tmp/appmon.log"}
	config.ErrorOutputPaths = []string{"/var/tmp/appmon.error.log"}
	config.EncoderConfig.TimeKey = "time"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger, err := config.Build()
	if err != nil {
		// Fallback to stdout if file logging fails
		logger, _ = zap.NewProduction()
	}
	return logger
}

func runUpdate(cmd *cobra.Command, args []string) error {
	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	updater := infra.NewUpdater(Version, logger)

	// Check-only mode (not compatible with --local-binary)
	if checkOnly {
		if localBinaryPath != "" {
			return fmt.Errorf("--check and --local-binary cannot be used together")
		}
		current, latest, available, err := updater.CheckUpdate()
		if err != nil {
			return fmt.Errorf("failed to check for updates: %w", err)
		}

		fmt.Printf("Current version: %s\n", current)
		fmt.Printf("Latest version:  %s\n", latest)

		if available {
			fmt.Println("Update available!")
		} else {
			fmt.Println("Already up to date.")
		}
		return nil
	}

	// Local binary update mode
	if localBinaryPath != "" {
		fmt.Println("\n=== appmon Update (Local Binary) ===")
		fmt.Printf("Current version: %s\n", Version)
		fmt.Printf("Local binary:    %s\n", localBinaryPath)

		fmt.Println()
		fmt.Println("Starting update process...")
		fmt.Println("  • Creating rollback backup")
		fmt.Println("  • Stopping daemons")
		fmt.Println("  • Installing binary")
		fmt.Println("  • Updating backups")
		fmt.Println("  • Restarting daemons")
		fmt.Println("  • Verifying health")
		fmt.Println()

		result, err := updater.PerformUpdateFromLocal(localBinaryPath)
		if err != nil {
			return fmt.Errorf("update failed: %w", err)
		}

		if result.RolledBack {
			fmt.Printf("\n✗ Update failed, rolled back to %s\n", result.PreviousVer)
			fmt.Printf("  Reason: %s\n", result.RollbackReason)
			return fmt.Errorf("update rolled back: %s", result.RollbackReason)
		}

		if result.Success {
			fmt.Printf("\n✓ Update successful!\n")
			fmt.Printf("  Version: %s\n", result.NewVer)
			fmt.Println("  All daemons running")
		}

		fmt.Println("=====================================")
		return nil
	}

	// GitHub update mode (default)
	fmt.Println("\n=== appmon Update ===")

	current, latest, available, err := updater.CheckUpdate()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	fmt.Printf("Current version: %s\n", current)
	fmt.Printf("Latest version:  %s\n", latest)

	if !available {
		fmt.Println("\nAlready up to date.")
		return nil
	}

	fmt.Println()
	fmt.Println("Starting update process...")
	fmt.Println("  • Creating rollback backup")
	fmt.Println("  • Downloading new version")
	fmt.Println("  • Stopping daemons")
	fmt.Println("  • Installing binary")
	fmt.Println("  • Updating backups")
	fmt.Println("  • Restarting daemons")
	fmt.Println("  • Verifying health")
	fmt.Println()

	result, err := updater.PerformUpdate()
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	if result.RolledBack {
		fmt.Printf("\n✗ Update failed, rolled back to %s\n", result.PreviousVer)
		fmt.Printf("  Reason: %s\n", result.RollbackReason)
		return fmt.Errorf("update rolled back: %s", result.RollbackReason)
	}

	if result.Success {
		fmt.Printf("\n✓ Update successful!\n")
		fmt.Printf("  Version: %s\n", result.NewVer)
		fmt.Println("  All daemons running")
	}

	fmt.Println("=====================")
	return nil
}

// runBlocklist prints the compiled-in blocklist and the active /etc/hosts
// blocklist side-by-side, plus a small diff if they disagree. Read-only:
// never modifies /etc/hosts even when divergence is detected.
func runBlocklist(cmd *cobra.Command, args []string) error {
	compiled := policy.DefaultDNSBlocklist
	hm := infra.NewHostsManager()
	active, err := hm.ActiveBlocklist()
	if err != nil {
		// /etc/hosts unreadable as the current user is a real possibility;
		// surface it as informational, not as a hard error.
		fmt.Printf("\n=== appmon Blocklist ===\n")
		fmt.Printf("Compiled into binary: %d hostnames\n", len(compiled))
		fmt.Printf("Active in /etc/hosts: unable to read (%v)\n", err)
		fmt.Println()
		fmt.Println("Compiled hostnames:")
		for _, h := range compiled {
			fmt.Printf("  %s\n", h)
		}
		return nil
	}

	fmt.Printf("\n=== appmon Blocklist ===\n")
	fmt.Printf("Compiled into binary: %d hostnames\n", len(compiled))
	fmt.Printf("Active in /etc/hosts: %d hostnames\n", len(active))

	compiledSet := map[string]struct{}{}
	for _, h := range compiled {
		compiledSet[h] = struct{}{}
	}
	activeSet := map[string]struct{}{}
	for _, h := range active {
		activeSet[h] = struct{}{}
	}

	var missingFromActive, extraInActive []string
	for h := range compiledSet {
		if _, ok := activeSet[h]; !ok {
			missingFromActive = append(missingFromActive, h)
		}
	}
	for h := range activeSet {
		if _, ok := compiledSet[h]; !ok {
			extraInActive = append(extraInActive, h)
		}
	}

	if len(missingFromActive) == 0 && len(extraInActive) == 0 {
		fmt.Println("Status:               in sync ✓")
	} else {
		fmt.Println("Status:               DIVERGENT (watcher will reconcile within ~60s)")
		if len(missingFromActive) > 0 {
			fmt.Printf("  Missing from /etc/hosts (%d):\n", len(missingFromActive))
			for _, h := range missingFromActive {
				fmt.Printf("    %s\n", h)
			}
		}
		if len(extraInActive) > 0 {
			fmt.Printf("  Extra in /etc/hosts (%d):\n", len(extraInActive))
			for _, h := range extraInActive {
				fmt.Printf("    %s\n", h)
			}
		}
	}

	fmt.Println()
	fmt.Println("Compiled hostnames (the permanent ban list):")
	for _, h := range compiled {
		fmt.Printf("  %s\n", h)
	}
	fmt.Println("=========================")
	return nil
}

func runVersion(cmd *cobra.Command, args []string) {
	if jsonOutput {
		fmt.Printf(`{"version":"%s","commit":"%s","build_time":"%s"}`+"\n",
			Version, Commit, BuildTime)
	} else {
		fmt.Printf("appmon %s (commit: %s, built: %s)\n",
			Version, Commit, BuildTime)
	}
}

// cleanupOldStaticPlist removes the pre-v0.5.0 static plist (com.focusd.appmon.plist)
// if it exists and the new randomized plist is different.
func cleanupOldStaticPlist(execMode *infra.ExecModeConfig) {
	oldLabel := infra.DefaultLaunchdLabel
	currentLabel := infra.GetLaunchdLabel()
	if oldLabel == currentLabel {
		return // Not using randomized label yet
	}

	oldPlistPath := filepath.Join(execMode.PlistDir, oldLabel+".plist")
	if _, err := os.Stat(oldPlistPath); err == nil {
		_ = exec.Command("launchctl", "unload", oldPlistPath).Run()
		_ = os.Remove(oldPlistPath)
		fmt.Printf("Removed old plist: %s\n", oldLabel)
	}
}

// rebuildExecModeWithLabel updates ExecModeConfig to use a randomized plist label.
func rebuildExecModeWithLabel(config *infra.ExecModeConfig, label string) *infra.ExecModeConfig {
	config.PlistPath = filepath.Join(config.PlistDir, label+".plist")
	return config
}

// openEncryptedRegistry initializes the encrypted registry for the given execution mode.
// Creates the data directory, generates or loads the encryption key, and opens the DB.
func openEncryptedRegistry(execMode *infra.ExecModeConfig, pm domain.ProcessManager) (*infra.EncryptedRegistry, error) {
	keyProvider := infra.NewFileKeyProvider(execMode.DataDir)
	key, err := infra.EnsureKey(keyProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure encryption key: %w", err)
	}
	reg, err := infra.NewEncryptedRegistry(execMode.DataDir, key, pm)
	if err != nil {
		return nil, fmt.Errorf("failed to open encrypted registry: %w", err)
	}
	return reg, nil
}

// printLiveDaemonList prints a compact view of daemons discovered via ps.
// Always uses real PIDs and observed paths — never registry data — so it's
// safe to call from any mode/uid and exposes mode mismatches cleanly.
func printLiveDaemonList(live []infra.LiveDaemon) {
	if len(live) == 0 {
		return
	}
	fmt.Println("\nLive daemon processes (from ps):")
	for _, d := range live {
		role := d.Role
		if role == "" {
			role = "?"
		}
		fmt.Printf("  - pid=%d role=%s path=%s\n", d.PID, role, d.Path)
	}
}

// heartbeatStaleThreshold is the maximum age of last_heartbeat we treat as
// "still alive". Anything older means the daemon is stuck or dead — even
// if the kernel still owns the PID — so the spawn path will tear it down
// and respawn. Three minutes is generous: the watcher heartbeats every
// 30s, so 6 missed heartbeats in a row triggers recovery.
const heartbeatStaleThreshold = 3 * time.Minute

// heartbeatFresh returns true when the registry's last_heartbeat is within
// heartbeatStaleThreshold of now. A zero timestamp is treated as stale.
func heartbeatFresh(lastHeartbeatUnix int64) bool {
	if lastHeartbeatUnix == 0 {
		return false
	}
	return time.Since(time.Unix(lastHeartbeatUnix, 0)) <= heartbeatStaleThreshold
}

// cleanupStaleDaemonProcesses kills any leftover daemon processes that
// could conflict with a fresh spawn. Runs unconditionally at the spawn
// step of runStart — idempotent when nothing's stale.
//
// Two passes via shared helpers in infra:
//   - infra.FindLegacyAppmonDaemons (basename "appmon" + daemon --role
//     argv) catches pre-relocation ghosts.
//   - Relocator.FindProcessesUsingDir catches relocated daemons whose
//     registry entry timed out.
//
// Both passes skip the current CLI's PID. Returns silently — best effort.
func cleanupStaleDaemonProcesses(pm domain.ProcessManager) {
	self := pm.GetCurrentPID()
	kill := func(pid int) {
		if pid == self {
			return
		}
		_ = pm.Kill(pid)
	}

	if pids, err := infra.FindLegacyAppmonDaemons(); err == nil {
		for _, pid := range pids {
			kill(pid)
		}
	}

	relocator := infra.NewRelocator(infra.GetRealUserHome())
	if pids, err := relocator.FindProcessesUsingDir(); err == nil {
		for _, pid := range pids {
			kill(pid)
		}
	}
}

// fileExists returns true if the path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// getInstalledVersion returns the version of the binary at the given path.
// Returns empty string if binary doesn't exist or version can't be determined.
func getInstalledVersion(binaryPath string) string {
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return ""
	}

	cmd := exec.Command(binaryPath, "version")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Parse "appmon X.Y.Z (...)" format
	parts := strings.Fields(string(output))
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// isNewerVersion returns true if current is newer than installed
func isNewerVersion(current, installed string) bool {
	if installed == "" {
		return true // No installed version → current is "newer"
	}

	currentParts := strings.Split(current, ".")
	installedParts := strings.Split(installed, ".")

	maxLen := len(currentParts)
	if len(installedParts) > maxLen {
		maxLen = len(installedParts)
	}

	for i := 0; i < maxLen; i++ {
		var currentNum, installedNum int

		if i < len(currentParts) {
			currentNum, _ = strconv.Atoi(currentParts[i])
		}
		if i < len(installedParts) {
			installedNum, _ = strconv.Atoi(installedParts[i])
		}

		if currentNum > installedNum {
			return true
		}
		if currentNum < installedNum {
			return false
		}
	}
	return false // Equal versions
}

// detectAndCleanupOtherModeDaemons surveys the OTHER mode (user vs system)
// for leftover artifacts and removes them. Runs unconditionally on every
// `appmon start` — even when no other-mode plist is present — because a
// half-cleaned other-mode install (stale binary at the other location,
// stale registry under the other DataDir) is exactly what causes
// `appmon status` to read the wrong DB and lie to the user.
//
// What gets cleaned up:
//   - Other-mode plist (unload + rm)
//   - Other-mode binary at the canonical path for that mode
//   - Other-mode data dir (encrypted registry + key) — root deletes
//     `/var/lib/appmon`; user deletes `~/.appmon`
//
// We don't touch the relocator cache dir (`~/.cache/.com.apple.xpc.*/`):
// it's mode-agnostic and reaped by the watcher's orphan reaper anyway.
//
// The function tolerates permission errors: a user-mode `appmon start`
// can't `rm /var/lib/appmon`, but that's fine — the dir isn't writable
// or readable by the user, so it can't cause status drift either.
func detectAndCleanupOtherModeDaemons(execMode *infra.ExecModeConfig, pm domain.ProcessManager) error {
	otherMode, plistPattern, binaryPath, dataDir := otherModePaths(execMode)
	otherModeDescription := string(otherMode)

	// Step 1: find and unload other-mode plists.
	matches, err := filepath.Glob(plistPattern)
	if err != nil {
		return fmt.Errorf("failed to glob for other mode plists: %w", err)
	}

	plistFound := len(matches) > 0
	binaryFound := fileExists(binaryPath)
	dataDirFound := fileExists(dataDir)

	if !plistFound && !binaryFound && !dataDirFound {
		// Nothing to do — clean slate.
		return nil
	}

	if plistFound {
		fmt.Printf("Detected %s mode daemons running, switching to %s mode...\n",
			otherModeDescription, execMode.Mode)

		// Kill all appmon processes except current CLI
		pids, _ := pm.FindByName("appmon")
		for _, pid := range pids {
			if pid != pm.GetCurrentPID() {
				_ = pm.Kill(pid)
			}
		}

		for _, plistPath := range matches {
			_ = exec.Command("launchctl", "unload", plistPath).Run()
			if err := os.Remove(plistPath); err == nil {
				fmt.Printf("Removed stale %s-mode plist: %s\n", otherModeDescription, filepath.Base(plistPath))
			}
		}
	}

	// Step 2: remove stale binary in the other mode's install location.
	// Idempotent — only fires when the file actually exists.
	if binaryFound {
		if err := os.Remove(binaryPath); err == nil {
			fmt.Printf("Removed stale %s-mode binary: %s\n", otherModeDescription, binaryPath)
		}
	}

	// Step 3: remove the other mode's data dir (registry, key, backups).
	// This is the bit that fixes "appmon status lies because it reads
	// the wrong DB" — without it, a stale registry from a former
	// session can sit around indefinitely and confuse the read path.
	if dataDirFound {
		if err := os.RemoveAll(dataDir); err == nil {
			fmt.Printf("Removed stale %s-mode data dir: %s\n", otherModeDescription, dataDir)
		}
	}

	// Give any killed processes time to fully exit before we proceed
	// with the spawn step. Skipping the wait when nothing was killed.
	if plistFound {
		time.Sleep(1 * time.Second)
	}

	return nil
}

// otherModePaths returns the canonical artifact paths for the mode that is
// NOT the one we're starting in. Splitting this out keeps the cleanup
// logic readable and makes it trivial to test against fake homes.
func otherModePaths(execMode *infra.ExecModeConfig) (
	otherMode infra.ExecMode,
	plistPattern string,
	binaryPath string,
	dataDir string,
) {
	home := infra.GetRealUserHome()
	if execMode.Mode == infra.ExecModeSystem {
		// Starting system → clean up user-mode footprint.
		otherMode = infra.ExecModeUser
		plistPattern = filepath.Join(home, "Library/LaunchAgents/com.apple.*.plist")
		binaryPath = filepath.Join(home, ".local", "bin", "appmon")
		dataDir = filepath.Join(home, ".appmon")
		return
	}
	// Starting user → clean up system-mode footprint.
	otherMode = infra.ExecModeSystem
	plistPattern = "/Library/LaunchDaemons/com.apple.*.plist"
	binaryPath = "/usr/local/bin/appmon"
	dataDir = "/var/lib/appmon"
	return
}
