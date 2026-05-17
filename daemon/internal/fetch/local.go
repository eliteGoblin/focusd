// Package fetch implements core.Fetcher: resolve the latest version and
// install a verified platform binary. Every binary is Ed25519-verified
// (sig package) BEFORE it is placed in the store — a download that
// fails verification is never run.
package fetch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// Local fetches from a directory laid out as a fake release feed:
//
//	<Dir>/latest             text file: the latest version tag
//	<Dir>/<version>/platform a SIGNED binary (program ++ 64-byte trailer)
//
// Used for deterministic e2e without network.
type Local struct{ Dir string }

func (l *Local) ResolveLatest(context.Context) (string, error) {
	b, err := os.ReadFile(filepath.Join(l.Dir, "latest"))
	if err != nil {
		return "", fmt.Errorf("fetch/local: read latest: %w", err)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("fetch/local: empty latest")
	}
	return v, nil
}

func (l *Local) EnsureBinary(_ context.Context, st *core.Store, version string) error {
	src := filepath.Join(l.Dir, version, "platform")
	ok, err := sig.VerifyFile(src)
	if err != nil {
		return fmt.Errorf("fetch/local: verify %s: %w", src, err)
	}
	if !ok {
		return fmt.Errorf("fetch/local: %s failed signature verification — refusing", version)
	}
	return placeVerified(src, st.BinPath(version))
}

// placeVerified copies an already-verified file to dst atomically with
// executable mode.
func placeVerified(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
