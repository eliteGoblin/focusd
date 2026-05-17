//go:build !darwin

package osadapter

import "errors"

// ErrUnsupported: launchd lifecycle is macOS-only. Linux/Windows land
// later behind this same package API.
var ErrUnsupported = errors.New("osadapter: launchd lifecycle is macOS-only")

func Install(Spec) error             { return ErrUnsupported }
func Uninstall(bool) error           { return ErrUnsupported }
func EnsureAll(Spec) ([]Role, error) { return nil, ErrUnsupported }
func IsLoaded(bool, Role) bool       { return false }
