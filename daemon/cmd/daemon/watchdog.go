package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/osadapter"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// doWatchdog is the out-of-band recovery rail (FEATURE 12 / ADR-0016), run
// once a minute by a root cron entry from a SECOND copy of this binary that
// lives outside the mesh workdir. It checks for a healthy mesh and, if it was
// totally torn down, rebuilds it LOCALLY (the normal install path, fresh
// roster — NO github fetch). Quiet healthy no-op when the mesh is intact.
//
// Surface:
//
//	daemon watchdog -v vX.Y.Z
//
// -v pins the platform version the rebuilt mesh should run (the watchdog
// recovers locally, so it must be told which version to pin — it does NOT
// resolve "latest" from GitHub).
func doWatchdog(args []string) int {
	if !osSupportsLaunchd() {
		// Non-darwin: no cron rail / no launchd mesh — nothing to do.
		return 0
	}
	fs := flag.NewFlagSet("watchdog", flag.ContinueOnError)
	desired := fs.String("v", "", "pinned desired platform version to rebuild with (e.g. v0.9.0)")
	_ = fs.Parse(args)
	if *desired == "" || !isValidVersionTag(*desired) {
		// A bad/missing version can't safely reach WriteDesired; refuse to
		// rebuild rather than pin a garbage version. Quiet (cron eats output).
		fmt.Fprintln(os.Stderr, "watchdog: -v vX.Y.Z required (strict semver)")
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "watchdog: executable:", err)
		return 1
	}
	m := mode.Resolve()
	return runWatchdog(self, m, *desired,
		func() (osadapter.CurInstall, error) {
			return osadapter.FindCurrentInstall(m, sig.VerifyFile)
		},
		func(spec *osadapter.Spec) error {
			return installMesh(self, spec, *desired)
		},
	)
}

// runWatchdog is the injectable core: find the current install, and if the
// mesh is NOT complete (all roles present), rebuild it via install. Seams
// (find / install) are injected so the decision is unit-tested with a Verifier
// fake and a recording install — no real launchctl. Returns the process exit
// code (always 0 in the healthy path; cron ignores it anyway).
func runWatchdog(
	self string, m mode.Mode, desired string,
	find func() (osadapter.CurInstall, error),
	install func(*osadapter.Spec) error,
) int {
	cur, err := find()
	if meshComplete(cur, err) {
		return 0 // healthy mesh — quiet no-op
	}
	// Mesh absent or incomplete → rebuild LOCALLY from this (copy) binary via
	// the normal install path (fresh roster per FEATURE 10). No github fetch.
	spec := osadapter.Spec{
		Mode:           m,
		SelfPath:       self,
		Interval:       workerHealInterval,
		EnsureInterval: osadapter.EnsureBackstopInterval,
	}
	if ierr := install(&spec); ierr != nil {
		fmt.Fprintln(os.Stderr, "watchdog: rebuild:", ierr)
		return 1
	}
	return 0
}

// meshComplete reports whether find() returned a fully-installed mesh — all
// AllRoles plists present. A discovery error or any missing role means the
// mesh needs a rebuild. This is the single health predicate the watchdog acts
// on (acceptance #1).
func meshComplete(cur osadapter.CurInstall, err error) bool {
	return err == nil && len(cur.PlistPaths) == len(osadapter.AllRoles)
}
