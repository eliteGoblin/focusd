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

func Install(Spec) error                               { return ErrUnsupported }
func Uninstall(bool) error                             { return ErrUnsupported }
func EnsureAll(Spec) ([]Role, error)                   { return nil, ErrUnsupported }
func IsLoaded(bool, Role) bool                         { return false }
func UninstallProd() ([]string, error)                 { return nil, ErrUnsupported }
func FindCurrentInstall(mode.Mode) (CurInstall, error) { return CurInstall{}, ErrUnsupported }
func SelfUpdateProd(CurInstall, Spec, []byte, time.Duration, time.Duration, bool) error {
	return ErrUnsupported
}

// CurInstall mirrors the darwin definition so cross-platform code that
// references it still compiles on non-darwin (where it is always zero).
type CurInstall struct {
	Mode       mode.Mode
	Base       string
	Workdir    string
	BinaryPath string
	PlistPaths []string
	Labels     []string
}
