//go:build darwin

package osadapter

import (
	"testing"
	"time"
)

// TestSelfUpdate_ReapsPreSeededOrphan (FEATURE 25, Element 2 — Test #2): an
// in-place daemon replacement with a REAL pre-seeded orphan platform under the
// sandbox support root. The old daemon's platform child reparents to launchd and
// survives the swap's bootout; the post-swap seam reaps it. After a successful
// self-update the orphan is gone. Every kill is gated on a positive existence
// check of the exact orphan first.
func TestSelfUpdate_ReapsPreSeededOrphan(t *testing.T) {
	root := reapRoot(t)
	orphanPID := spawnFakePlatform(t, root, ".oldgen", "v0.16.3")

	// Build a minimal in-place-replacement scenario (old mesh loaded → new mesh
	// healthy → old booted out) with the existing self-update fakes.
	c, fs := newFakeCtl(), newFakeFS()
	cur := curInstall()
	for i, l := range cur.Labels {
		c.loadedSet[l] = true
		fs.files[cur.PlistPaths[i]] = "OLD"
	}
	b := &fakeBinPlacer{bytes: map[string][]byte{cur.BinaryPath: {0x01}}}
	s := newSpec()
	loaded, pids := allHealthy(s)
	p := newFakeProber(loaded, pids)

	// GATE: the orphan must genuinely exist before the swap.
	if !isAlive(orphanPID) {
		t.Fatal("pre-condition: pre-seeded orphan platform must be alive")
	}

	// The production afterSwap reaps same-mode orphans (keepPID unknown at swap
	// time → 0); here it anchors on the sandbox root so it only ever sees the
	// pre-seeded orphan.
	afterSwap := func() {
		_, _ = reapForeignPlatforms(root, 0, "", listPlatformProcs, resolvePlatformExecs, signedVerify(), killProc)
	}
	if err := SelfUpdate(cur, s, []byte("NEWBIN"), c, fs, p, &fakeBinPlacerWrap{b}, nil,
		2*time.Second, 5*time.Millisecond, false, afterSwap); err != nil {
		t.Fatalf("self-update: %v", err)
	}
	waitDead(t, orphanPID)
}
