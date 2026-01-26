package policy

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/user/focusd/app_mon/internal/domain"
)

// Dota2Policy implements AppPolicy for blocking Dota 2.
type Dota2Policy struct {
	homeDir string
}

// NewDota2Policy creates a new Dota 2 blocking policy.
func NewDota2Policy() *Dota2Policy {
	home, _ := os.UserHomeDir()
	return &Dota2Policy{homeDir: home}
}

// NewDota2PolicyWithHome creates a Dota 2 policy with a custom home directory (for testing).
func NewDota2PolicyWithHome(homeDir string) *Dota2Policy {
	return &Dota2Policy{homeDir: homeDir}
}

func (p *Dota2Policy) ID() string {
	return "dota2"
}

func (p *Dota2Policy) Name() string {
	return "Dota 2"
}

// ProcessPatterns returns Dota 2 process names to kill.
func (p *Dota2Policy) ProcessPatterns() []string {
	return []string{
		"dota2",
		"dota_osx64",
		"Dota 2",
		"dota2_launcher",
	}
}

// PathsToDelete returns all Dota 2-related paths to remove.
// Dota 2 is installed via Steam, so paths are under Steam's directory.
func (p *Dota2Policy) PathsToDelete() []string {
	steamBase := filepath.Join(p.homeDir, "Library/Application Support/Steam")

	return []string{
		// Main game folder (dota 2 beta is the actual folder name)
		filepath.Join(steamBase, "steamapps/common/dota 2 beta"),

		// Workshop content (custom games, mods)
		filepath.Join(steamBase, "steamapps/workshop/content/570"),

		// Shader caches
		filepath.Join(steamBase, "steamapps/shadercache/570"),

		// Download cache for Dota 2
		filepath.Join(steamBase, "steamapps/downloading/570"),

		// Dota 2 app manifest
		filepath.Join(steamBase, "steamapps/appmanifest_570.acf"),

		// Dota 2 Reborn specific
		filepath.Join(steamBase, "steamapps/common/dota 2"),

		// Any Dota downloads
		filepath.Join(p.homeDir, "Downloads/*[Dd]ota*.dmg"),
		filepath.Join(p.homeDir, "Downloads/*[Dd]ota*.zip"),
	}
}

func (p *Dota2Policy) ScanInterval() time.Duration {
	return DefaultScanInterval
}

func (p *Dota2Policy) PreEnforce(ctx context.Context) error {
	return nil
}

func (p *Dota2Policy) PostEnforce(ctx context.Context, result *domain.EnforcementResult) error {
	return nil
}

// Ensure Dota2Policy implements AppPolicy.
var _ AppPolicy = (*Dota2Policy)(nil)
