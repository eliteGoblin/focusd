// Behavior 4 (Bug 1) regression + FIX 2 (tighten-only): the platform's
// --workdir flow must, end to end, take the embedded default config and
// overlay the on-disk override at <workdir>/config.yaml — new IDs are
// appended, untouched defaults pass through, and (tighten-only, "no inside
// door handle") an override that tries to DISABLE a baked-enabled default is
// REFUSED: the protection stays enabled all the way into the running App.
// This proves the wiring `parseCommon` → defaultconfig.LoadWithOverrides →
// app.Bootstrap carries the merged config into the running App, not just that
// the loader works in isolation (which the unit tests already cover).
package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/app"
	"github.com/eliteGoblin/focusd/platform/internal/core/config"
	"github.com/eliteGoblin/focusd/platform/internal/defaultconfig"
	"github.com/eliteGoblin/focusd/platform/internal/testutil"
)

func TestPlatformBootstrapMergesWorkdirOverride(t *testing.T) {
	// Baseline: what jobs ship in the embedded default? The test asserts
	// against this set rather than hard-coded names so a future default
	// addition doesn't silently break the regression.
	defaultCfg, _, err := defaultconfig.LoadWithOverrides("")
	if err != nil {
		t.Fatalf("load embedded default: %v", err)
	}
	if len(defaultCfg.Jobs) < 2 {
		t.Fatalf("embedded default must have >=2 jobs to exercise both "+
			"override-disable and untouched-passthrough; got %d", len(defaultCfg.Jobs))
	}
	target := defaultCfg.Jobs[0]     // baked-enabled; we'll try (and fail) to disable it
	toPreserve := defaultCfg.Jobs[1] // we'll leave this one untouched
	if !target.Enabled {
		t.Fatalf("test precondition: %q must be baked enabled to exercise the refusal", target.ID)
	}

	// Simulate the daemon's --workdir layout: a tempdir holding the
	// user's override config.yaml. parseCommon's path resolution is
	// `filepath.Join(workdir, "config.yaml")`, so we write exactly there.
	workdir := t.TempDir()
	overridePath := filepath.Join(workdir, "config.yaml")
	override := "" +
		"platform:\n" +
		"  log_level: debug\n" +
		"jobs:\n" +
		"  - id: " + target.ID + "\n" +
		"    plugin: " + target.Plugin + "\n" +
		"    enabled: false\n" +
		"    schedule: \"@every 1h\"\n" +
		"    timeout: 1s\n" +
		"    retry: 0\n" +
		"    allow_overlap: false\n" +
		"    config: {}\n" +
		"  - id: my-custom-job\n" +
		"    plugin: kill-steam\n" +
		"    enabled: true\n" +
		"    schedule: \"@every 30m\"\n" +
		"    timeout: 5s\n" +
		"    retry: 0\n" +
		"    allow_overlap: false\n" +
		"    config: {}\n"
	if err := os.WriteFile(overridePath, []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	// Mirror parseCommon: load the merged config, then hand it to
	// Bootstrap via opts.Config (the exact path the platform CLI uses).
	// Warnings are not asserted here — Behavior 4 is about job-shape
	// merging, not the typo-detection warning channel.
	merged, _, err := defaultconfig.LoadWithOverrides(overridePath)
	if err != nil {
		t.Fatalf("LoadWithOverrides: %v", err)
	}

	// Bootstrap needs an adapter; the fake keeps everything in tempdirs
	// so no real OS paths or root are required.
	fa := testutil.NewFakeAdapter(t.TempDir())
	a, err := app.Bootstrap(app.Options{Adapter: fa, Config: merged})
	if err != nil {
		t.Fatalf("Bootstrap with merged config: %v", err)
	}
	defer a.Close()

	// 1. Job count = defaults + 1 (the new my-custom-job is appended;
	//    same-ID overrides stay as replace-by-ID, not delete).
	gotJobs := a.Config.Jobs
	wantCount := len(defaultCfg.Jobs) + 1
	if len(gotJobs) != wantCount {
		t.Fatalf("merged job count = %d, want %d (defaults=%d + 1 appended)",
			len(gotJobs), wantCount, len(defaultCfg.Jobs))
	}

	// 2. TIGHTEN-ONLY: the override tried to disable a baked-enabled default;
	//    end-to-end the App must still see it ENABLED (the disable is refused),
	//    while its non-disable field (schedule) still merged.
	targeted := findJob(gotJobs, target.ID)
	if targeted == nil {
		t.Fatalf("targeted default %q vanished from merged config", target.ID)
	}
	if !targeted.Enabled {
		t.Fatalf("tighten-only: override must NOT disable baked-enabled %q end-to-end", target.ID)
	}
	if targeted.Schedule != "@every 1h" {
		t.Fatalf("non-disable override fields must still merge, schedule=%q", targeted.Schedule)
	}

	// 3. The untouched default passes through with its original enabled
	//    flag (i.e. the override does NOT bleed into IDs it didn't name).
	preserved := findJob(gotJobs, toPreserve.ID)
	if preserved == nil {
		t.Fatalf("untouched default %q must still be present", toPreserve.ID)
	}
	if preserved.Enabled != toPreserve.Enabled {
		t.Fatalf("untouched default %q.Enabled changed: got %v want %v",
			toPreserve.ID, preserved.Enabled, toPreserve.Enabled)
	}

	// 4. The new custom job is appended (Merge() appends; ordering of the
	//    new ID at the tail is part of the contract).
	if last := gotJobs[len(gotJobs)-1]; last.ID != "my-custom-job" {
		t.Fatalf("new override job must be appended at the tail; got tail id %q", last.ID)
	}

	// 5. Platform-level override took effect end-to-end.
	if a.Config.Platform.LogLevel != "debug" {
		t.Fatalf("Platform.LogLevel override not applied: %q", a.Config.Platform.LogLevel)
	}
}

func findJob(jobs []config.Job, id string) *config.Job {
	for i := range jobs {
		if jobs[i].ID == id {
			return &jobs[i]
		}
	}
	return nil
}
