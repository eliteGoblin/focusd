//go:build !e2e

package main

import "flag"

// Release builds expose only the user/system install modes (chosen by
// euid: sudo → system, else user). The throwaway `test` install mode is
// NOT compiled into a release binary, so `--test-mode` does not exist
// and the binary cannot be made easily removable. The e2e harness builds
// with `-tags e2e` (see testmode_e2e.go) to opt it back in.

const testModeCompiledIn = false

// registerTestMode is a no-op here: no `--test-mode` flag is registered,
// and test mode is never requested.
func registerTestMode(*flag.FlagSet) func() bool { return func() bool { return false } }
