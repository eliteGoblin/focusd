//go:build !e2e

package main

// workdirOverride (release build) NEVER honors a caller-supplied workdir. It
// ignores the --workdir flag AND the WorkdirEnvKey environment variable and
// returns "" unconditionally, so the caller always falls through to
// osadapter.DeriveWorkdir (self-derived from the binary's own location). This is
// what keeps the disguised platform child's workdir off BOTH argv and the
// environment — neither `ps` nor `ps -E` can surface it. The --workdir flag
// stays registered (so direct invocation does not error) but is inert.
func workdirOverride(string) string { return "" }
