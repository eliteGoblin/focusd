package main

import (
	"runtime"
	"testing"
)

// TestPlatformAssetDerived locks the deterministic name: releases are
// published as platform-{GOOS}-{GOARCH}, so the daemon must derive exactly
// that — never an operator-supplied value.
func TestPlatformAssetDerived(t *testing.T) {
	want := "platform-" + runtime.GOOS + "-" + runtime.GOARCH
	if got := platformAsset(); got != want {
		t.Fatalf("platformAsset() = %q, want %q", got, want)
	}
}

// TestParseDerivesAssetAndKeepsLaterArgs is the self-heal regression guard.
// An already-baked plist argv carries a stale/WRONG --asset (the old bug
// baked the daemon asset name as the platform asset → 404 → no recovery).
// parse() must (a) IGNORE that baked value and derive the correct platform
// asset, and (b) still parse the args that FOLLOW --asset (--roster/--r/
// --mesh) — fully removing the flag would make flag.Parse choke on the
// unknown flag and silently drop the rest, breaking an existing install.
func TestParseDerivesAssetAndKeepsLaterArgs(t *testing.T) {
	o := parse("run", []string{
		"--asset", "daemon-darwin-arm64", // WRONG (daemon asset) — must be ignored
		"--roster", "a.x,b.y,c.z",
		"--r", "b",
		"--mesh",
	})
	if o.asset != platformAsset() {
		t.Fatalf("asset = %q, want derived %q (baked --asset must be ignored)", o.asset, platformAsset())
	}
	if len(o.roster) != 3 || o.roster[0] != "a.x" || o.roster[2] != "c.z" {
		t.Fatalf("roster not parsed — args after --asset were dropped: %v", o.roster)
	}
	if o.role != "b" {
		t.Fatalf("role = %q, want b — args after --asset were dropped", o.role)
	}
	if !o.mesh {
		t.Fatal("--mesh not parsed — args after --asset were dropped")
	}
}
