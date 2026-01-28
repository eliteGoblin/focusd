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
)

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

				// Remove system plist
				systemPlist := "/Library/LaunchDaemons/com.focusd.appmon.plist"
				if _, err := os.Stat(systemPlist); err == nil {
					_ = exec.Command("launchctl", "unload", systemPlist).Run()
					_ = os.Remove(systemPlist)
					fmt.Println("Removed system LaunchDaemon")
				}

				// Clear registry (including lock file)
				registry := infra.NewFileRegistry(pm)
				_ = registry.Clear()
				// Also remove lock file which may have root ownership
				registryPath := registry.GetRegistryPath()
				_ = os.Remove(registryPath + ".lock")

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

	// Initialize components
	pm := infra.NewProcessManager()
	registry := infra.NewFileRegistry(pm)

	// Check if system mode is running (for mode switch detection)
	systemPlistExists := false
	if _, err := os.Stat("/Library/LaunchDaemons/com.focusd.appmon.plist"); err == nil {
		systemPlistExists = true
	}

	// Check if already running - handle mode switching
	entry, _ := registry.GetAll()
	if entry != nil {
		watcherAlive := pm.IsRunning(entry.WatcherPID)
		guardianAlive := pm.IsRunning(entry.GuardianPID)

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

	// Install LaunchAgent/LaunchDaemon based on execution mode (idempotent)
	launchdManager := infra.NewLaunchdManager(execMode)

	// Cleanup plist from wrong mode (handles user↔system migration)
	if err := launchdManager.CleanupOtherMode(); err != nil {
		fmt.Printf("Warning: Could not cleanup other mode plist: %v\n", err)
	}

	if !launchdManager.IsInstalled() {
		// Fresh install
		if err := launchdManager.Install(binaryPath); err != nil {
			fmt.Printf("Warning: Could not install %s: %v\n", execMode.Mode, err)
			fmt.Println("         (appmon will still run, but won't auto-start)")
		} else {
			if execMode.IsRoot {
				fmt.Println("Installed LaunchDaemon for system-wide auto-start")
			} else {
				fmt.Println("Installed LaunchAgent for auto-start on login")
			}
		}
	} else if launchdManager.NeedsUpdate(binaryPath) {
		// Exists but outdated - update in place
		if err := launchdManager.Update(binaryPath); err != nil {
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
	registry := infra.NewFileRegistry(pm)
	backupManager := infra.NewBackupManager()

	fmt.Println("\n=== appmon Status ===")

	entry, err := registry.GetAll()
	if err != nil || entry == nil {
		fmt.Println("Status: NOT RUNNING")
		fmt.Println("\nRun 'appmon start' to enable protection.")
		return nil
	}

	watcherAlive := pm.IsRunning(entry.WatcherPID)
	guardianAlive := pm.IsRunning(entry.GuardianPID)

	if watcherAlive && guardianAlive {
		fmt.Println("Status: RUNNING (fully protected)")
	} else if watcherAlive || guardianAlive {
		fmt.Println("Status: DEGRADED (partial protection)")
		if !watcherAlive {
			fmt.Println("        Watcher is down (will be restarted by guardian)")
		}
		if !guardianAlive {
			fmt.Println("        Guardian is down (will be restarted by watcher)")
		}
	} else {
		fmt.Println("Status: NOT RUNNING")
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
		execMode := backupConfig.ExecMode
		if execMode == "" {
			execMode = "user"
		}
		fmt.Printf("Execution mode: %s\n", execMode)

		// Check if plist exists (don't show path for security)
		if _, err := os.Stat(backupConfig.PlistPath); err == nil {
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

	// Blocked apps
	fmt.Println("\nBlocked applications:")
	policyRegistry := policy.NewRegistry()
	for _, p := range policyRegistry.GetAll() {
		fmt.Printf("  - %s\n", p.Name())
	}

	// Freedom protection status
	fmt.Println("\nFreedom protection:")
	freedomProtector := infra.NewFreedomProtector(pm, nil) // nil logger for status
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
	registry := infra.NewFileRegistry(pm)

	// Determine execution mode - use explicit flag if provided, otherwise auto-detect
	var execMode *infra.ExecModeConfig
	if daemonMode == "user" {
		execMode = infra.GetUserModeConfig()
	} else if daemonMode == "system" {
		execMode = infra.DetectExecMode() // Will return system if running as root
	} else {
		execMode = infra.DetectExecMode() // Auto-detect: root → LaunchDaemon, user → LaunchAgent
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

func runVersion(cmd *cobra.Command, args []string) {
	if jsonOutput {
		fmt.Printf(`{"version":"%s","commit":"%s","build_time":"%s"}`+"\n",
			Version, Commit, BuildTime)
	} else {
		fmt.Printf("appmon %s (commit: %s, built: %s)\n",
			Version, Commit, BuildTime)
	}
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
