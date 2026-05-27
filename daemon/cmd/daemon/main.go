// Command daemon is the focusd Layer-1 daemon: it ensures the correct
// platform version is always running (download from GitHub Releases,
// Ed25519-verify, start; roll back a crash-looping version).
//
//	daemon run     [--workdir D] [--interval 10s] [--github owner/repo --asset NAME | --release-dir D]
//	daemon once    same flags; one reconcile tick then exit
//	daemon update  re-resolve latest now and roll forward
//	daemon version print daemon version
//	daemon install / uninstall   (darwin launchd; see osadapter)
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/fetch"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/osadapter"
	"github.com/eliteGoblin/focusd/daemon/internal/platformsvc"
	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
	"github.com/eliteGoblin/focusd/daemon/internal/uninstallgate"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "version", "-v", "--version":
		fmt.Println("focusd-daemon", version)
		return 0
	case "run":
		return loop(args[1:], false)
	case "once":
		return loop(args[1:], true)
	case "update":
		return doUpdate(args[1:])
	case "ensure":
		return doEnsure(args[1:])
	case "install":
		return doInstall(args[1:])
	case "uninstall":
		return doUninstall(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: daemon run|once|update|version|install|uninstall [flags]")
}

type opts struct {
	workdir    string
	interval   time.Duration
	github     string
	asset      string
	releaseDir string
	healthy    time.Duration
	unhealthy  time.Duration
	role       string
	testMode   bool
	mesh       bool
	base       string
}

func (o opts) spec(self string) osadapter.Spec {
	return osadapter.Spec{
		Mode: o.modeVal(), SelfPath: self, Workdir: o.workdir,
		Github: o.github, Asset: o.asset, Interval: o.interval,
		Base: o.base,
	}
}

// modeVal is the install mode for a running mesh member: test when the
// installer baked --test-mode-flag into the plist, otherwise the real
// deployment mode resolved from euid (sudo → system, else user).
func (o opts) modeVal() mode.Mode {
	if o.testMode {
		return mode.Test
	}
	return mode.Resolve()
}

func parse(name string, args []string) opts {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	wd := fs.String("workdir", defaultWorkdir(), "daemon work directory")
	iv := fs.Duration("interval", 10*time.Second, "reconcile interval")
	gh := fs.String("github", "eliteGoblin/focusd", "owner/repo for releases")
	as := fs.String("asset", "", "release asset filename (per os/arch)")
	rd := fs.String("release-dir", "", "use a local fake release dir instead of GitHub")
	hd := fs.Duration("healthy", 5*time.Second, "alive longer than this ⇒ promote good")
	ud := fs.Duration("unhealthy", 3*time.Second, "exit sooner than this ⇒ crashed")
	rl := fs.String("r", "a", "mesh role: a|b")
	tm := fs.String("test-mode-flag", "false", "use test-mode launchd labels")
	mesh := fs.Bool("mesh", false, "self-heal the launchd mesh (set only by the installer)")
	mb := fs.String("mesh-base", "", "disguised launchd label base (set by the installer)")
	_ = fs.Parse(args)
	return opts{*wd, *iv, *gh, *as, *rd, *hd, *ud, *rl, *tm == "true", *mesh, *mb}
}

func defaultWorkdir() string {
	h, _ := os.UserHomeDir()
	return h + "/Library/Application Support/focusd-daemon"
}

func build(o opts) (*core.Executor, *slog.Logger) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	st := &core.Store{Dir: o.workdir}
	var f core.Fetcher
	if o.releaseDir != "" {
		f = &fetch.Local{Dir: o.releaseDir}
	} else {
		f = &fetch.GitHub{Repo: o.github, Asset: o.asset}
	}
	p := platformsvc.New(o.workdir)
	if o.healthy > 0 {
		p.Healthy = o.healthy
	}
	if o.unhealthy > 0 {
		p.Unhealthy = o.unhealthy
	}
	return core.NewExecutor(st, f, p, log), log
}

func loop(args []string, once bool) int {
	o := parse("run", args)
	e, log := build(o)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	self, _ := os.Executable()
	spec := o.spec(self)

	tick := func() {
		a, err := e.Tick(ctx)
		if err != nil {
			log.Error("tick error", "err", err)
		} else {
			log.Info("tick", "role", o.role, "action", string(a.Kind),
				"target", a.Target, "note", a.Note)
		}
		// Mesh self-heal: only when launched as part of an installed
		// mesh (--mesh, set solely by the installer). A plain
		// `daemon run` (e2e/foreground) never touches launchd.
		if o.mesh {
			if rec, eerr := osadapter.EnsureAll(spec); eerr != nil {
				log.Warn("ensure-all", "err", eerr)
			} else if len(rec) > 0 {
				log.Info("mesh recreated", "roles", rec)
			}
		}
	}

	tick()
	if once {
		return 0
	}
	t := time.NewTicker(o.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("daemon stopping")
			return 0
		case <-t.C:
			tick()
		}
	}
}

// doUpdate writes the desired platform version into the store. Two
// forms:
//
//	daemon update vX.Y.Z   — write desired=vX.Y.Z, no network call.
//	daemon update          — resolve "Latest" from GitHub ONCE; write.
//	                         Exits non-zero on resolve failure. No retry.
//
// The reconcile loop sees the new desired on its next tick and downloads
// + swaps via the normal EnsureRunning path (which is fetch-then-stop
// — see executor.go). This command itself is a thin write + (optional)
// one-shot resolve; it does NOT loop on ticks or wipe state.
func doUpdate(args []string) int {
	o := parse("update", args)
	_, log := build(o)

	// Separate flag and positional args. The first non-flag arg, if
	// present, is the explicit version.
	var explicit string
	for _, a := range args {
		if len(a) > 0 && a[0] != '-' {
			explicit = a
			break
		}
	}

	st := &core.Store{Dir: o.workdir}

	if explicit != "" {
		if err := st.WriteDesired(explicit); err != nil {
			log.Error("write desired failed", "err", err)
			return 1
		}
		log.Info("desired written", "version", explicit, "note", "no network call")
		return 0
	}

	// No version given → one-shot GH "Latest". On any failure, exit
	// non-zero immediately; no retry, no tick loop. The reconcile loop
	// never re-tries this resolve on its own.
	ctx := context.Background()
	f := &fetch.GitHub{Repo: o.github, Asset: o.asset}
	v, err := f.ResolveLatest(ctx)
	if err != nil {
		log.Error("resolve latest from GitHub failed", "err", err,
			"hint", "pass an explicit version: daemon update vX.Y.Z")
		return 1
	}
	if err := st.WriteDesired(v); err != nil {
		log.Error("write desired failed", "err", err)
		return 1
	}
	log.Info("desired written", "version", v, "note", "resolved from GitHub")
	return 0
}

// osSupportsLaunchd reports whether launchd install/uninstall is
// available. Guards against filesystem side effects (relocation into the
// mode SupportRoot, plist writes) on platforms where osadapter.Install /
// Uninstall is only an ErrUnsupported stub.
func osSupportsLaunchd() bool { return runtime.GOOS == "darwin" }

func doInstall(args []string) int {
	if !osSupportsLaunchd() {
		fmt.Fprintln(os.Stderr, "install: unsupported on", runtime.GOOS, "(darwin/launchd only)")
		return 1
	}
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	wd := fs.String("workdir", defaultWorkdir(), "daemon work directory")
	gh := fs.String("github", "eliteGoblin/focusd", "owner/repo")
	as := fs.String("asset", "", "release asset filename")
	iv := fs.Duration("interval", 10*time.Second, "reconcile interval")
	desired := fs.String("v", "",
		"REQUIRED desired platform version (e.g. v0.9.0) — the daemon does NOT auto-resolve from GitHub")
	wantTest := registerTestMode(fs) // --test-mode only under -tags e2e
	_ = fs.Parse(args)
	if *desired == "" {
		fmt.Fprintln(os.Stderr,
			"install: -v vX.Y.Z is required (the daemon does NOT auto-update; pin a version explicitly)")
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "executable:", err)
		return 1
	}
	// The daemon decides the install mode at bootstrap: test (e2e builds
	// only), else system when run as root (sudo), else user.
	m := mode.Resolve()
	if wantTest() {
		m = mode.Test
	}
	spec := osadapter.Spec{
		Mode: m, SelfPath: self, Workdir: *wd,
		Github: *gh, Asset: *as, Interval: *iv,
	}
	if m != mode.Test {
		// user/system: self-relocate into a hidden random workdir under
		// this mode's Application Support root (user → ~/Library,
		// system → /Library) + a disguised per-install label base. Test
		// mode stays fixed/deterministic + uses the given workdir so e2e
		// is safe & removable.
		home, _ := os.UserHomeDir()
		wd := relocate.HiddenWorkdir(mode.SupportRoot(m, home))
		reloc, rerr := relocate.RelocateInto(self, wd)
		if rerr != nil {
			fmt.Fprintln(os.Stderr, "relocate:", rerr)
			return 1
		}
		spec.SelfPath = reloc
		spec.Workdir = wd
		spec.Base = relocate.RandomBase()
		fmt.Printf("relocated → %s (mode %s, base %s)\n", reloc, m, spec.Base)
	}
	if err := osadapter.Install(spec); err != nil {
		fmt.Fprintln(os.Stderr, "install:", err)
		return 1
	}
	// Pin the desired platform version BEFORE the launchd mesh comes up,
	// so the first reconcile tick has a target and never enters the
	// "no desired" Blocked state. The daemon does not auto-resolve.
	st := &core.Store{Dir: spec.Workdir}
	if err := st.WriteDesired(*desired); err != nil {
		fmt.Fprintln(os.Stderr, "write desired:", err)
		return 1
	}
	fmt.Printf("installed (desired platform = %s)\n", *desired)
	return 0
}

// doEnsure is the ensurer role (StartInterval): recreate any missing
// mesh entry (A/B/ensure), then exit. The mesh's periodic backstop.
func doEnsure(args []string) int {
	o := parse("ensure", args)
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "executable:", err)
		return 1
	}
	rec, eerr := osadapter.EnsureAll(o.spec(self))
	if eerr != nil {
		fmt.Fprintln(os.Stderr, "ensure:", eerr)
		return 1
	}
	fmt.Printf("ensure ok (recreated=%v)\n", rec)
	return 0
}

func doUninstall(args []string) int {
	if !osSupportsLaunchd() {
		fmt.Fprintln(os.Stderr, "uninstall: unsupported on", runtime.GOOS, "(darwin/launchd only)")
		return 1
	}
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	wantTest := registerTestMode(fs) // --test-mode only under -tags e2e
	abort := fs.Bool("abort", false, "discard uninstall-cooldown progress and keep the protection")
	_ = fs.Parse(args)
	if wantTest() {
		// e2e/test installs bypass the commitment gate entirely so CI
		// teardown is deterministic and never blocks for hours.
		if err := osadapter.Uninstall(true); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall:", err)
			return 1
		}
		fmt.Println("uninstalled (test-mode)")
		return 0
	}

	// PROD (user/system): the commitment gate runs before any teardown.
	// It turns an impulsive removal into a deliberate, multi-hour ritual
	// (transcribe → wait 2h → transcribe → wait 4h → transcribe). See
	// internal/uninstallgate and daemon_design.md.
	home, herr := os.UserHomeDir()
	if herr != nil {
		// Without a real home the gate state path would be relative and
		// land in CWD — silently weakening the gate. Fail instead.
		fmt.Fprintln(os.Stderr, "uninstall: cannot resolve home directory:", herr)
		return 1
	}
	gpath := uninstallgate.StatePath(mode.Resolve(), home)
	if *abort {
		if err := uninstallgate.Clear(gpath); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall --abort:", err)
			return 1
		}
		fmt.Println("uninstall aborted — cooldown reset, protection kept.")
		return 0
	}
	if code, proceed := runUninstallGate(gpath); !proceed {
		return code
	}

	// Gate satisfied — labels are randomized, find ours by Ed25519 sig.
	removed, err := osadapter.UninstallProd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall:", err)
		return 1
	}
	_ = uninstallgate.Clear(gpath) // gate state dies with the install
	fmt.Printf("uninstalled (prod): %v\n", removed)
	return 0
}

// runUninstallGate advances the commitment gate one interaction. It
// returns (exitCode, proceed): proceed=true means all steps are done and
// the caller should perform the real teardown; proceed=false means the
// caller should return exitCode now (waiting, rejected, or step accepted
// but more steps remain).
func runUninstallGate(gpath string) (code int, proceed bool) {
	st := uninstallgate.Load(gpath, time.Now())
	o := uninstallgate.Evaluate(st, time.Now())

	if o.Kind == uninstallgate.Wait {
		fmt.Printf("Uninstall is on a cooldown. Come back in %s.\n",
			o.Remaining.Round(time.Minute))
		return 1, false
	}

	if o.Kind == uninstallgate.Transcribe {
		ref := uninstallgate.Passage(o.Step)
		fmt.Printf("Uninstall step %d of %d.\n\n"+
			"Type the passage below EXACTLY, by hand. This is intentional "+
			"friction: if the urge to uninstall is impulsive it will fade "+
			"long before you finish. Finish with Ctrl-D on a blank line.\n\n"+
			"----- BEGIN PASSAGE -----\n%s\n----- END PASSAGE -----\n\n",
			o.Step, uninstallgate.TotalSteps, ref)

		start := time.Now()
		typed, rerr := io.ReadAll(os.Stdin)
		if rerr != nil {
			// A read failure must not be treated as a transcription
			// attempt — don't advance or save.
			fmt.Fprintln(os.Stderr, "could not read input:", rerr, "(no progress lost)")
			return 1, false
		}
		ok, why := uninstallgate.Accept(string(typed), ref, time.Since(start))
		if !ok {
			fmt.Fprintln(os.Stderr, "not accepted:", why, "(no progress lost — try again)")
			return 1, false
		}
		st = uninstallgate.Advance(st, time.Now())
		if err := uninstallgate.Save(gpath, st); err != nil {
			fmt.Fprintln(os.Stderr, "gate save:", err)
			return 1, false
		}
		o = uninstallgate.Evaluate(st, time.Now())
		if o.Kind != uninstallgate.Proceed {
			fmt.Printf("Step accepted. Come back in %s to continue.\n",
				o.Remaining.Round(time.Minute))
			return 0, false
		}
		// step 3 just completed → fall through to teardown
	}

	return 0, true // o.Kind == Proceed
}
