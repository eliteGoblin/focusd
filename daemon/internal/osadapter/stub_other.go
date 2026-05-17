//go:build !darwin

package osadapter

import "errors"

// ErrUnsupported is returned by lifecycle ops on non-macOS hosts.
// macOS is the only supported target now; Linux/Windows land later
// behind this same interface.
var ErrUnsupported = errors.New("osadapter: launchd lifecycle is macOS-only")

func Install(Spec) error   { return ErrUnsupported }
func Uninstall(bool) error { return ErrUnsupported }
func IsLoaded(bool) bool   { return false }
