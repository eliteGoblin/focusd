//go:build !darwin

package osadapter

import (
	"errors"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// ErrUnsupported: launchd lifecycle is macOS-only. Linux/Windows land
// later behind this same package API.
var ErrUnsupported = errors.New("osadapter: launchd lifecycle is macOS-only")

func Install(Spec) error               { return ErrUnsupported }
func Uninstall(bool) error             { return ErrUnsupported }
func EnsureAll(Spec) ([]Role, error)   { return nil, ErrUnsupported }
func IsLoaded(bool, Role) bool         { return false }
func UninstallProd() ([]string, error) { return nil, ErrUnsupported }

// Verifier is the signature-check seam (no-op on non-darwin since
// FindCurrentInstall returns ErrUnsupported here).
type Verifier func(path string) (bool, error)

func FindCurrentInstall(mode.Mode, Verifier) (CurInstall, error) {
	return CurInstall{}, ErrUnsupported
}

// Generation mirrors the darwin definition so cross-platform code that
// references it compiles on non-darwin (where discovery is unsupported).
type Generation struct {
	BinaryPath string
	Workdir    string
	Labels     []string
	PlistPaths []string
}

// DeadGeneration mirrors the darwin definition (FEATURE 17 follow-up) so
// cross-platform code that references it compiles on non-darwin.
type DeadGeneration struct {
	BinaryPath string
	Workdir    string
	Labels     []string
	PlistPaths []string
}

func DiscoverAllGenerations(mode.Mode, Verifier) ([]Generation, []DeadGeneration, error) {
	return nil, nil, ErrUnsupported
}

// RetireOtherGenerations is a no-op on non-darwin (no launchd mesh to retire).
// It returns (0, nil) so the cross-platform install path treats it as "nothing
// to do" rather than surfacing ErrUnsupported on every install.
func RetireOtherGenerations(mode.Mode, string) (int, error) { return 0, nil }

// SweepOrphanWorkdirs is a no-op on non-darwin (no launchd mesh / generation
// workdirs to sweep). Returns (0, nil) so the cross-platform install path treats
// it as "nothing to do" rather than surfacing ErrUnsupported on every install.
func SweepOrphanWorkdirs(mode.Mode, string) (int, error) { return 0, nil }
func MeshStatus(mode.Mode) (loaded, total int, found bool, err error) {
	return 0, 0, false, ErrUnsupported
}
func SelfUpdateProd(CurInstall, Spec, []byte, time.Duration, time.Duration, bool) error {
	return ErrUnsupported
}

// CurInstall mirrors the darwin definition so cross-platform code that
// references it still compiles on non-darwin (where it is always zero).
type CurInstall struct {
	Mode       mode.Mode
	Roster     []string
	Workdir    string
	BinaryPath string
	Interval   time.Duration
	PlistPaths []string
	Labels     []string
}
