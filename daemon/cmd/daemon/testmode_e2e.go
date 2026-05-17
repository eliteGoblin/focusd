//go:build e2e

package main

import "flag"

// This file is compiled ONLY with `-tags e2e` (CI / local e2e). It opts
// the throwaway `test` install mode back in: `daemon install --test-mode`
// (and `uninstall --test-mode`) use fixed labels + the caller-supplied
// workdir so an e2e run is deterministic and safely removable. A release
// binary is built WITHOUT this tag, so it has no `--test-mode` flag.

const testModeCompiledIn = true

// registerTestMode adds the `--test-mode` flag and returns a getter for
// whether it was set.
func registerTestMode(fs *flag.FlagSet) func() bool {
	t := fs.Bool("test-mode", false, "[e2e builds only] easily-removable test install (fixed labels + given workdir)")
	return func() bool { return *t }
}
