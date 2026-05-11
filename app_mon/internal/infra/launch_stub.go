package infra

import (
	"os"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// SecretKeyLaunchStub stores the path of the LaunchAgent's relocated stub.
const SecretKeyLaunchStub = "launch_stub_path"

// EnsureLaunchStub returns a stable relocated path the LaunchAgent's plist
// references via ProgramArguments[0]. The stub is a hard link / copy of
// mainBinaryPath at a randomized system-looking basename so macOS's Login
// Items UI displays the obfuscated name instead of "appmon".
//
// If a stub path is already stored in the secret store and its content
// matches the current main binary (by SHA256), it is reused. Otherwise a
// fresh stub is generated, the stale one is unlinked, and the new path is
// persisted.
//
// On any failure the caller should fall back to mainBinaryPath; the
// LaunchAgent will still function, but Login Items will show "appmon".
func EnsureLaunchStub(mainBinaryPath, home string, store domain.SecretStore) (string, error) {
	r := NewRelocator(home)

	if stored, _ := store.GetSecret(SecretKeyLaunchStub); stored != "" {
		if stubMatches(stored, mainBinaryPath) {
			return stored, nil
		}
		_ = os.Remove(stored)
	}

	stub, err := r.Relocate(mainBinaryPath)
	if err != nil {
		return "", err
	}
	if err := store.SetSecret(SecretKeyLaunchStub, stub); err != nil {
		_ = os.Remove(stub)
		return "", err
	}
	return stub, nil
}

// stubMatches returns true when both files exist and share the same SHA256.
// Used to detect when the main binary has been updated and the stub points
// to stale content (e.g., a hard link to a now-replaced inode).
func stubMatches(stubPath, mainBinaryPath string) bool {
	if _, err := os.Stat(stubPath); err != nil {
		return false
	}
	stubSHA, err := computeSHA256(stubPath)
	if err != nil {
		return false
	}
	mainSHA, err := computeSHA256(mainBinaryPath)
	if err != nil {
		return false
	}
	return stubSHA == mainSHA
}
