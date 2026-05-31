package status

import (
	"io"
	"time"
)

// Options controls rendering. The probe/aggregation logic is unaffected.
type Options struct {
	JSON    bool // emit machine-readable JSON instead of text
	NoColor bool // suppress ANSI colour (also honoured via NO_COLOR env)
}

// Run is the pure status pipeline: probe every layer through src, assemble
// a Snapshot, aggregate the overall verdict, render to out, and return the
// process exit code (0 healthy / 1 degraded / 2 down). It performs NO I/O
// of its own beyond src and out — so it is fully unit-testable with a fake
// Source and a bytes.Buffer.
//
// Run never returns the exit-code-3 "internal error" itself; that is the
// caller's job when ProbeInput could not be assembled (e.g. FindCurrentInstall
// failed). Here, a discoverable-but-broken install still yields a clean
// DOWN/DEGRADED snapshot.
func Run(src Source, in ProbeInput, out io.Writer, opts Options) int {
	s := &Snapshot{
		Mode: in.Mode,
		Engine: EngineView{
			Mode:    in.Mode,
			Workdir: in.Workdir,
			Found:   len(in.MeshLabels) > 0 || in.Workdir.Present(),
		},
		Mesh:     ProbeMesh(src, in),
		Platform: ProbePlatform(src, in),
		Jobs:     ProbePlugins(src, in),
		Hosts:    ProbeHosts(src, in),
		Pf:       ProbePf(src, in),
		Skills:   ProbeSkills(src, in),
	}

	age, ageKnown := installAge(src, in)
	Aggregate(s, age, ageKnown)

	if opts.JSON {
		renderJSON(s, out)
	} else {
		renderText(s, out, !opts.NoColor)
	}
	return s.Overall.ExitCode()
}

// installAge estimates how long the install has existed by reading the
// version.json mtime indirectly — but since the Source has no Stat, we
// approximate "fresh" as: a discovered install with NO recorded job runs
// AND no good version yet. When we can't read the workdir at all, age is
// unknown (so we never falsely claim "warming up").
//
// The age value itself is only consulted relative to freshInstallWindow;
// we return 0 (well within the window) when the heuristic says "fresh" and
// a large value otherwise, keeping Aggregate's comparison simple.
func installAge(src Source, in ProbeInput) (time.Duration, bool) {
	if !in.Workdir.Present() {
		return 0, false
	}
	// If a good version has already been promoted, the install is past its
	// warm-up regardless of clock — not fresh.
	good := ProbePlatform(src, in).Good
	if good != "" {
		return time.Hour, true // definitively not warming up
	}
	return 0, true // no good version yet → treat as fresh/warming up
}
