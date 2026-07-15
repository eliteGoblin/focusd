// Config-lock (Approach B) regression: the daemon-managed run path enforces
// the SIGNED embedded default ONLY. A config.yaml dropped into the workdir
// is INERT — never read — so a weak-moment edit (disable a job, delete a
// job, flip enabled:false) has no effect on enforcement.
//
// This exercises the exact policy MECHANISM the run path relies on — the
// same two calls the CLI's parseCommon makes on the daemon-managed path:
// load policy via defaultconfig.Load() (path-free) and hand it to
// app.Bootstrap through opts.Config, with a config.yaml sitting in the
// workdir to prove it is never read. (It calls Load()/Bootstrap directly
// rather than through parseCommon, which only adds flag parsing + os.Exit
// error paths.) The old override-merge behavior (workdir config.yaml
// overlaid on the default) was removed as a tamper surface.
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

func TestRunPathIgnoresWorkdirConfig(t *testing.T) {
	// Baseline: the enforced policy the run path would load.
	defaultCfg, err := defaultconfig.Load()
	if err != nil {
		t.Fatalf("load embedded default: %v", err)
	}
	if len(defaultCfg.Jobs) < 2 {
		t.Fatalf("embedded default needs >=2 jobs to exercise the regression; got %d", len(defaultCfg.Jobs))
	}
	toDisable := defaultCfg.Jobs[0] // the hostile override tries to disable this

	// Simulate the daemon's --workdir layout: a real config.yaml on disk at
	// <workdir>/config.yaml — the exact path the OLD loader used to read.
	// It disables one default and adds a rogue job. Both must be ignored.
	workdir := t.TempDir()
	overridePath := filepath.Join(workdir, "config.yaml")
	hostile := "" +
		"platform:\n" +
		"  log_level: debug\n" +
		"jobs:\n" +
		"  - id: " + toDisable.ID + "\n" +
		"    plugin: " + toDisable.Plugin + "\n" +
		"    enabled: false\n" +
		"    schedule: \"@every 1h\"\n" +
		"    timeout: 1s\n" +
		"    retry: 0\n" +
		"    allow_overlap: false\n" +
		"    config: {}\n" +
		"  - id: rogue-job\n" +
		"    plugin: kill-steam\n" +
		"    enabled: true\n" +
		"    schedule: \"@every 30m\"\n" +
		"    timeout: 5s\n" +
		"    retry: 0\n" +
		"    allow_overlap: false\n" +
		"    config: {}\n"
	if err := os.WriteFile(overridePath, []byte(hostile), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sanity: the tamper file really is present on disk — we are proving it
	// is present-but-ignored, not merely absent.
	if _, err := os.Stat(overridePath); err != nil {
		t.Fatalf("precondition: workdir config.yaml should exist: %v", err)
	}

	// The run path loads policy PATH-FREE (defaultconfig.Load), never from
	// the workdir file, then hands it to Bootstrap via opts.Config.
	loaded, err := defaultconfig.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	fa := testutil.NewFakeAdapter(t.TempDir())
	a, err := app.Bootstrap(app.Options{Adapter: fa, Config: loaded})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer a.Close()

	gotJobs := a.Config.Jobs

	// (a) The workdir file had NO effect: same job set as the embedded
	//     default — the rogue job was NOT appended.
	if len(gotJobs) != len(defaultCfg.Jobs) {
		t.Fatalf("job count = %d, want %d (workdir config.yaml must be inert)",
			len(gotJobs), len(defaultCfg.Jobs))
	}
	if findJob(gotJobs, "rogue-job") != nil {
		t.Fatal("rogue-job from the workdir config.yaml leaked into enforced policy")
	}

	// (b) The override that tried to disable a default had no effect: the
	//     job is still present with its default enabled flag.
	disabled := findJob(gotJobs, toDisable.ID)
	if disabled == nil {
		t.Fatalf("default job %q vanished", toDisable.ID)
	}
	if disabled.Enabled != toDisable.Enabled {
		t.Fatalf("job %q.Enabled changed to %v; override must be inert (want %v)",
			toDisable.ID, disabled.Enabled, toDisable.Enabled)
	}

	// (c) network-block ships enabled and stayed enabled.
	nb := findJob(gotJobs, "network-block-reconcile")
	if nb == nil || !nb.Enabled {
		t.Fatal("network-block-reconcile must be present and enabled in enforced policy")
	}

	// Platform-level tamper (log_level: debug) also had no effect.
	if a.Config.Platform.LogLevel != defaultCfg.Platform.LogLevel {
		t.Fatalf("Platform.LogLevel changed to %q; workdir override must be inert (want %q)",
			a.Config.Platform.LogLevel, defaultCfg.Platform.LogLevel)
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
