//go:build integration

package infra

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// TestHybridBackup_LocalRestore tests restoring from local backup when binary is deleted.
// Run with: go test -tags=integration -v -run TestHybridBackup_LocalRestore ./internal/infra
func TestHybridBackup_LocalRestore(t *testing.T) {
	// Create temp home directory
	tmpHome, err := os.MkdirTemp("", "appmon-test-home-")
	if err != nil {
		t.Fatalf("failed to create temp home: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	// Create a fake binary
	binaryDir := filepath.Join(tmpHome, ".local", "bin")
	if err := os.MkdirAll(binaryDir, 0755); err != nil {
		t.Fatalf("failed to create binary dir: %v", err)
	}

	binaryPath := filepath.Join(binaryDir, "appmon")
	binaryContent := []byte("#!/bin/sh\necho 'fake appmon v1.0.0'")
	if err := os.WriteFile(binaryPath, binaryContent, 0755); err != nil {
		t.Fatalf("failed to write binary: %v", err)
	}

	// Create backup manager with test home
	logger, _ := zap.NewDevelopment()
	bm := NewBackupManagerWithLogger(tmpHome, logger)

	// Setup backups
	err = bm.SetupBackupsWithMode(binaryPath, "1.0.0", "test", ExecModeUser)
	if err != nil {
		t.Fatalf("failed to setup backups: %v", err)
	}

	// Verify backups were created
	config, err := bm.GetConfig()
	if err != nil {
		t.Fatalf("failed to get config: %v", err)
	}
	t.Logf("Created %d local backups", len(config.BackupPaths))

	// Delete the main binary
	if err := os.Remove(binaryPath); err != nil {
		t.Fatalf("failed to remove binary: %v", err)
	}
	t.Log("Deleted main binary")

	// Verify binary is gone
	if _, err := os.Stat(binaryPath); !os.IsNotExist(err) {
		t.Fatal("binary should not exist")
	}

	// Run VerifyAndRestore - should restore from local backup
	restored, err := bm.VerifyAndRestore()
	if err != nil {
		t.Fatalf("VerifyAndRestore failed: %v", err)
	}

	if !restored {
		t.Fatal("expected binary to be restored")
	}

	// Verify binary was restored
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		t.Fatal("binary should have been restored")
	}

	// Verify content matches
	restoredContent, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("failed to read restored binary: %v", err)
	}

	if string(restoredContent) != string(binaryContent) {
		t.Fatal("restored content does not match original")
	}

	t.Log("SUCCESS: Binary restored from local backup")
}

// TestHybridBackup_GitHubFallback tests downloading from GitHub when local backups are gone.
// This test requires network access and uses the real GitHub release.
// Run with: go test -tags=integration -v -run TestHybridBackup_GitHubFallback ./internal/infra
func TestHybridBackup_GitHubFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping GitHub fallback test in short mode")
	}

	// Create temp home directory
	tmpHome, err := os.MkdirTemp("", "appmon-test-home-")
	if err != nil {
		t.Fatalf("failed to create temp home: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	// Create binary directory
	binaryDir := filepath.Join(tmpHome, ".local", "bin")
	if err := os.MkdirAll(binaryDir, 0755); err != nil {
		t.Fatalf("failed to create binary dir: %v", err)
	}

	binaryPath := filepath.Join(binaryDir, "appmon")

	// Create a fake binary first
	binaryContent := []byte("fake binary content")
	if err := os.WriteFile(binaryPath, binaryContent, 0755); err != nil {
		t.Fatalf("failed to write binary: %v", err)
	}

	// Create backup manager with test home
	logger, _ := zap.NewDevelopment()
	bm := NewBackupManagerWithLogger(tmpHome, logger)

	// Setup backups (this creates local backups with the fake binary)
	err = bm.SetupBackupsWithMode(binaryPath, "0.0.1", "test", ExecModeUser)
	if err != nil {
		t.Fatalf("failed to setup backups: %v", err)
	}

	config, err := bm.GetConfig()
	if err != nil {
		t.Fatalf("failed to get config: %v", err)
	}

	// Delete the main binary
	if err := os.Remove(binaryPath); err != nil {
		t.Fatalf("failed to remove binary: %v", err)
	}
	t.Log("Deleted main binary")

	// Delete ALL local backups
	for _, backupPath := range config.BackupPaths {
		if err := os.RemoveAll(filepath.Dir(backupPath)); err != nil {
			t.Logf("failed to remove backup %s: %v", backupPath, err)
		}
	}
	t.Log("Deleted all local backups")

	// Run VerifyAndRestore - should fall back to GitHub
	t.Log("Running VerifyAndRestore (expecting GitHub download)...")
	restored, err := bm.VerifyAndRestore()
	if err != nil {
		t.Fatalf("VerifyAndRestore failed: %v", err)
	}

	if !restored {
		t.Fatal("expected binary to be restored from GitHub")
	}

	// Verify binary was restored
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		t.Fatal("binary should have been restored from GitHub")
	}

	// Verify it's an actual binary (larger than our fake)
	info, err := os.Stat(binaryPath)
	if err != nil {
		t.Fatalf("failed to stat restored binary: %v", err)
	}

	if info.Size() < 1000 {
		t.Fatalf("restored binary too small (%d bytes), expected real binary", info.Size())
	}

	t.Logf("SUCCESS: Binary restored from GitHub (size: %d bytes)", info.Size())

	// Verify local backups were refreshed
	newConfig, err := bm.GetConfig()
	if err != nil {
		t.Fatalf("failed to get updated config: %v", err)
	}

	t.Logf("Local backups refreshed: %d backups created", len(newConfig.BackupPaths))
}

// TestGitHubDownloader_DownloadLatest tests downloading from GitHub releases.
// Run with: go test -tags=integration -v -run TestGitHubDownloader ./internal/infra
func TestGitHubDownloader_DownloadLatest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping GitHub download test in short mode")
	}

	downloader := NewGitHubDownloader()

	// Get latest version
	version, err := downloader.GetLatestVersion()
	if err != nil {
		t.Fatalf("failed to get latest version: %v", err)
	}
	t.Logf("Latest version: %s", version)

	// Download to temp
	tmpPath, err := downloader.DownloadToTemp()
	if err != nil {
		t.Fatalf("failed to download: %v", err)
	}
	defer os.RemoveAll(filepath.Dir(tmpPath))

	// Verify file exists and is executable
	info, err := os.Stat(tmpPath)
	if err != nil {
		t.Fatalf("failed to stat downloaded binary: %v", err)
	}

	t.Logf("Downloaded binary: %s (size: %d bytes)", tmpPath, info.Size())

	if info.Size() < 1000 {
		t.Fatal("downloaded binary too small")
	}

	// Verify it's executable
	if info.Mode()&0111 == 0 {
		t.Fatal("downloaded binary is not executable")
	}

	t.Log("SUCCESS: Binary downloaded from GitHub")
}
