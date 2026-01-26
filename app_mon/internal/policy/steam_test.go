package policy

import (
	"testing"
)

func TestSteamPolicy_ID(t *testing.T) {
	p := NewSteamPolicy()
	if p.ID() != "steam" {
		t.Errorf("expected ID 'steam', got '%s'", p.ID())
	}
}

func TestSteamPolicy_Name(t *testing.T) {
	p := NewSteamPolicy()
	if p.Name() != "Steam" {
		t.Errorf("expected Name 'Steam', got '%s'", p.Name())
	}
}

func TestSteamPolicy_ProcessPatterns(t *testing.T) {
	p := NewSteamPolicy()
	patterns := p.ProcessPatterns()

	if len(patterns) == 0 {
		t.Error("expected at least one process pattern")
	}

	// Check for known patterns
	expectedPatterns := map[string]bool{
		"Steam":          false,
		"steam_osx":      false,
		"steamwebhelper": false,
	}

	for _, pattern := range patterns {
		if _, ok := expectedPatterns[pattern]; ok {
			expectedPatterns[pattern] = true
		}
	}

	for pattern, found := range expectedPatterns {
		if !found {
			t.Errorf("expected pattern '%s' not found", pattern)
		}
	}
}

func TestSteamPolicy_PathsToDelete(t *testing.T) {
	// Use custom home for predictable paths
	p := NewSteamPolicyWithHome("/Users/testuser")
	paths := p.PathsToDelete()

	if len(paths) == 0 {
		t.Error("expected at least one path")
	}

	// Check for known paths
	foundApplications := false
	foundLibrary := false

	for _, path := range paths {
		if path == "/Applications/Steam.app" {
			foundApplications = true
		}
		if path == "/Users/testuser/Library/Application Support/Steam" {
			foundLibrary = true
		}
	}

	if !foundApplications {
		t.Error("expected /Applications/Steam.app in paths")
	}
	if !foundLibrary {
		t.Error("expected ~/Library/Application Support/Steam in paths")
	}
}

func TestSteamPolicy_ScanInterval(t *testing.T) {
	p := NewSteamPolicy()
	interval := p.ScanInterval()

	if interval != DefaultScanInterval {
		t.Errorf("expected interval %v, got %v", DefaultScanInterval, interval)
	}
}
