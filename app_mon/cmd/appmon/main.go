// Package main is the CLI entry point for appmon.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
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
	// Version info (set via ldflags)
	Version   = "0.2.0"
	Commit    = "dev"
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

// Hidden daemon command - used for self-exec when spawning daemons
var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Hidden: true,
	RunE:   runDaemon,
}

var (
	daemonRole string
	daemonName string
	jsonOutput bool
)

func init() {
	daemonCmd.Flags().StringVar(&daemonRole, "role", "", "Daemon role (watcher/guardian)")
	daemonCmd.Flags().StringVar(&daemonName, "name", "", "Obfuscated process name")
	versionCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output version info as JSON")

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(scanCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(daemonCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	// Detect execution mode (sudo vs non-sudo)
	execMode := infra.DetectExecMode()

	fmt.Printf("Execution mode: %s\n", execMode.Mode)
	if execMode.IsRoot {
		fmt.Println("Running as root - will install as LaunchDaemon (system-wide)")
	} else {
		fmt.Println("Running as user - will install as LaunchAgent (user-space)")
	}

	// Initialize components
	pm := infra.NewProcessManager()
	registry := infra.NewFileRegistry(pm)

	// Check if already running
	entry, _ := registry.GetAll()
	if entry != nil {
		watcherAlive := pm.IsRunning(entry.WatcherPID)
		guardianAlive := pm.IsRunning(entry.GuardianPID)

		if watcherAlive && guardianAlive {
			fmt.Println("appmon is already running (fully protected)")
			return nil
		}
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

	// Install LaunchAgent/LaunchDaemon based on execution mode
	launchdManager := infra.NewLaunchdManager(execMode)
	if !launchdManager.IsInstalled() {
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
	}

	// Start both daemons
	if err := daemon.StartBothDaemons(); err != nil {
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

	// Check backup config for execution mode
	backupConfig, err := backupManager.GetConfig()
	if err == nil {
		execMode := backupConfig.ExecMode
		if execMode == "" {
			execMode = "user"
		}
		fmt.Printf("\nExecution mode: %s\n", execMode)
		fmt.Printf("Binary path: %s\n", backupConfig.MainBinaryPath)
		fmt.Printf("Plist path: %s\n", backupConfig.PlistPath)

		// Check if plist exists
		if _, err := os.Stat(backupConfig.PlistPath); err == nil {
			fmt.Println("Auto-start: enabled")
		} else {
			fmt.Println("Auto-start: disabled (plist missing)")
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
	}

	// Initialize infrastructure
	pm := infra.NewProcessManager()
	fs := infra.NewFileSystemManager()
	registry := infra.NewFileRegistry(pm)
	execMode := infra.DetectExecMode() // Auto-detect: root → LaunchDaemon, user → LaunchAgent
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

func runVersion(cmd *cobra.Command, args []string) {
	if jsonOutput {
		fmt.Printf(`{"version":"%s","commit":"%s","build_time":"%s"}`+"\n",
			Version, Commit, BuildTime)
	} else {
		fmt.Printf("appmon %s (commit: %s, built: %s)\n",
			Version, Commit, BuildTime)
	}
}
