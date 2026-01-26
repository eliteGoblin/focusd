// Package fixtures provides test helpers for integration tests.
package fixtures

import (
	"os"
	"path/filepath"
)

// FakeSteamStructure creates a directory structure mimicking Steam installation.
type FakeSteamStructure struct {
	HomeDir string
}

// NewFakeSteamStructure creates a new fake Steam structure generator.
func NewFakeSteamStructure(homeDir string) *FakeSteamStructure {
	return &FakeSteamStructure{HomeDir: homeDir}
}

// Create creates the fake Steam directory structure.
func (f *FakeSteamStructure) Create() error {
	paths := []string{
		// Steam app (simulated)
		filepath.Join(f.HomeDir, "Applications/Steam.app/Contents/MacOS"),

		// Steam support files
		filepath.Join(f.HomeDir, "Library/Application Support/Steam/config"),
		filepath.Join(f.HomeDir, "Library/Application Support/Steam/steamapps/common"),

		// Dota 2 specific
		filepath.Join(f.HomeDir, "Library/Application Support/Steam/steamapps/common/dota 2 beta/game"),
		filepath.Join(f.HomeDir, "Library/Application Support/Steam/steamapps/workshop/content/570"),

		// Caches
		filepath.Join(f.HomeDir, "Library/Caches/com.valvesoftware.steam"),
	}

	for _, p := range paths {
		if err := os.MkdirAll(p, 0755); err != nil {
			return err
		}
		// Create a marker file to verify deletion
		markerFile := filepath.Join(p, ".marker")
		if err := os.WriteFile(markerFile, []byte("test"), 0644); err != nil {
			return err
		}
	}

	return nil
}

// Exists checks if the Steam directory structure exists.
func (f *FakeSteamStructure) Exists() bool {
	steamPath := filepath.Join(f.HomeDir, "Library/Application Support/Steam")
	_, err := os.Stat(steamPath)
	return err == nil
}

// Dota2Exists checks if the Dota 2 directory exists.
func (f *FakeSteamStructure) Dota2Exists() bool {
	dotaPath := filepath.Join(f.HomeDir, "Library/Application Support/Steam/steamapps/common/dota 2 beta")
	_, err := os.Stat(dotaPath)
	return err == nil
}

// GetPaths returns all paths that would be created.
func (f *FakeSteamStructure) GetPaths() []string {
	return []string{
		filepath.Join(f.HomeDir, "Applications/Steam.app"),
		filepath.Join(f.HomeDir, "Library/Application Support/Steam"),
		filepath.Join(f.HomeDir, "Library/Caches/com.valvesoftware.steam"),
	}
}

// Cleanup removes all fake Steam directories.
func (f *FakeSteamStructure) Cleanup() error {
	paths := []string{
		filepath.Join(f.HomeDir, "Applications"),
		filepath.Join(f.HomeDir, "Library"),
	}

	for _, p := range paths {
		os.RemoveAll(p)
	}
	return nil
}
