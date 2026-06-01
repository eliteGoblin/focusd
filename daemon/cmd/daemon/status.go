package main

import (
	"errors"
	"flag"
	"os"

	"github.com/eliteGoblin/focusd/daemon/internal/status"
)

// doStatus is the CLI dispatch for `daemon status`: a read-only health
// snapshot of the focusd install. It reports ONLY daemon-owned facts (mesh
// roles up, platform process alive, platform version) and delegates plugin
// detail to `platform status`, passing its output through (ADR-0012).
//
// Surface:
//
//	daemon status [--json] [--no-color] [--workdir DIR]
//
// --workdir is an optional override for the discovered (disguised) workdir;
// normally the install is discovered by Ed25519 signature and the operator
// never needs it. There is deliberately NO flag that re-exposes disguised
// identifiers (no --show-paths / --debug) — that would reopen the leak this
// command closes (feature 09 non-goals).
//
// Exit codes: 0 healthy/unknown · 1 degraded · 2 down · 3 internal error.
func doStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON")
	noColor := fs.Bool("no-color", false, "suppress ANSI colour")
	wd := fs.String("workdir", "", "override the discovered workdir (rarely needed)")
	if err := fs.Parse(args); err != nil {
		// --help/-h is a clean request, not a failure → exit 0. Any genuine
		// parse error is internal/usage (3), not DOWN (2).
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 3
	}

	snap, pd := status.Gather(*wd, *jsonOut)
	res := status.Assess(snap)

	color := !*noColor && os.Getenv("NO_COLOR") == ""
	if *jsonOut {
		status.RenderJSON(snap, res, pd, os.Stdout)
	} else {
		status.RenderText(snap, res, pd, os.Stdout, color)
	}
	return status.ExitCode(res.Verdict)
}
