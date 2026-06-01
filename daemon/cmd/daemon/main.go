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
	"regexp"
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

// versionTagRE matches strict semver release tags: `v1.2.3`, plus an
// optional pre-release segment (`-rc.1`, `-beta-foo`) and an optional
// build segment (`+abc123`). The leading `v` is mandatory. This is the
// ONLY shape accepted by `daemon install -v` and `daemon update <ver>`;
// anything else is rejected upfront so a malicious or fat-finger value
// like `v/../etc/passwd` or `vlatest` can't reach Store.WriteDesired
// and then become part of an on-disk binary path. (Copilot review.)
var versionTagRE = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[A-Za-z0-9][A-Za-z0-9.\-]*)?(\+[A-Za-z0-9][A-Za-z0-9.\-]*)?$`)

func isValidVersionTag(s string) bool { return versionTagRE.MatchString(s) }

// daemonVersionTagRE matches strict daemon release tags:
// `daemon-v1.2.3` + optional pre-release/build (same shape as
// versionTagRE, just with the `daemon-` prefix). This is the ONLY
// shape `daemon self-update <tag>` accepts — same path-traversal
// concern as versionTagRE.
var daemonVersionTagRE = regexp.MustCompile(`^daemon-v\d+\.\d+\.\d+(-[A-Za-z0-9][A-Za-z0-9.\-]*)?(\+[A-Za-z0-9][A-Za-z0-9.\-]*)?$`)

func isValidDaemonTag(s string) bool { return daemonVersionTagRE.MatchString(s) }

// githubRepoRE matches a plain `owner/repo` value for the --github
// flag. The owner+repo each must be a non-empty token of allowed chars
// — anything fancier (paths, query strings, URL fragments) is rejected
// upfront so the value can't subvert the GH API URL it's interpolated
// into: `https://api.github.com/repos/<owner>/<repo>/releases/...`.
// (Security-reviewer MEDIUM #2.)
var githubRepoRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

func isValidGithubRepo(s string) bool { return githubRepoRE.MatchString(s) }

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
	case "self-update":
		return doSelfUpdate(args[1:])
	case "status":
		return doStatus(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: daemon run|once|update|version|install|uninstall|self-update|status [flags]")
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
	// Use a dedicated FlagSet so we can correctly pick up the trailing
	// positional version after all flags (handles `--workdir=/x v1.0.0`
	// AND `--workdir /x v1.0.0` AND `v1.0.0`). The hand-rolled "first
	// arg without a `-` prefix" scan was wrong for `--flag value` form,
	// where `value` is non-flag but not the version. Go-review HIGH #2.
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	// --workdir defaults to "" (NOT the default path) so an explicit
	// override is detectable, matching `self-update`/`status`. Empty means
	// "discover the running install"; a non-empty value is an explicit
	// target the operator chose.
	wd := fs.String("workdir", "", "explicit daemon work directory (default: discover the running install)")
	gh := fs.String("github", "eliteGoblin/focusd", "owner/repo (for `update` with no version arg)")
	as := fs.String("asset", "", "release asset filename")
	_ = fs.Parse(args)
	explicit := fs.Arg(0) // optional positional version, e.g. v1.2.3

	// Resolve the target workdir. A real install relocates to a disguised,
	// random path, so by default we DISCOVER the running mesh's workdir
	// (same Ed25519 recognition `self-update` uses) — discovery WINS over
	// any stale install at the default location. An explicit --workdir is
	// always honored. The disguised path stays INTERNAL to this process; it
	// is never logged/printed. A discovery I/O error fails fast rather than
	// silently writing to the wrong place.
	workdir, derr := resolveUpdateWorkdir(*wd, defaultWorkdir(), discoverInstallWorkdir)
	if derr != nil {
		fmt.Fprintln(os.Stderr,
			"update: could not locate the install; re-run with sudo or pass --workdir")
		return 1
	}

	o := opts{workdir: workdir, github: *gh, asset: *as}
	_, log := build(o)

	st := &core.Store{Dir: o.workdir}

	if explicit != "" {
		// Strict tag validator — accepting any "v…" string would let
		// `v../etc/passwd` reach Store.WriteDesired and then become part
		// of the on-disk binary path. (Copilot review.)
		if !isValidVersionTag(explicit) {
			log.Error("update: version must be a strict semver tag like v0.9.0 or v1.2.3-rc.1",
				"got", explicit)
			return 2
		}
		if err := st.WriteDesired(explicit); err != nil {
			// Don't log raw err: the store path can be the disguised workdir.
			log.Error("write desired failed (store not writable; re-run with sudo?)")
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

// resolveUpdateWorkdir picks the workdir `daemon update` writes to. An
// explicit (non-default) --workdir is always honored. Otherwise, if a
// install. Precedence: an explicit (non-empty) --workdir is always
// honored; otherwise DISCOVERY of the real (possibly disguised/relocated)
// install WINS over the default location — a stale install left at the
// default must NOT shadow the running mesh (that bug wrote `desired` to the
// wrong place and the platform never upgraded). Only when discovery finds
// nothing do we fall back to defaultWd. A discovery I/O error is propagated
// so the caller fails fast instead of silently writing to the wrong place.
// Pure + injectable so the precedence is unit-tested without touching launchd.
func resolveUpdateWorkdir(explicitWd, defaultWd string, discover func() (string, error)) (string, error) {
	if explicitWd != "" {
		return explicitWd, nil // operator chose an explicit target
	}
	wd, err := discover()
	if err != nil {
		return "", err // I/O failure during discovery — fail fast
	}
	if wd != "" {
		return wd, nil // the real, running, discovered install wins
	}
	return defaultWd, nil // nothing discovered — use the default
}

// discoverInstallWorkdir finds the genuine focusd install for the current
// mode via Ed25519 recognition (nil verifier => sig.VerifyFile) and returns
// its workdir. ("", nil) means no install was found (a clean fallback);
// a non-nil error is a real filesystem failure. Never logs or prints the
// path.
func discoverInstallWorkdir() (string, error) {
	cur, err := osadapter.FindCurrentInstall(mode.Resolve(), nil)
	if err != nil {
		return "", err
	}
	return cur.Workdir, nil
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
	// Strict tag validator (same as `daemon update`): rejects path
	// separators and traversal components so `-v ../../etc/passwd` can't
	// escape the workdir/store layout downstream. (Copilot review.)
	if !isValidVersionTag(*desired) {
		fmt.Fprintln(os.Stderr,
			"install: -v must be a strict semver tag like v0.9.0 or v1.2.3-rc.1, got:", *desired)
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "executable:", err)
		return 1
	}
	// Single mesh per install (FEATURE 08 / ADR-0010). The operator's
	// invocation chooses ONE mode — there is never a second simultaneous
	// mesh:
	//
	//   sudo daemon install  → System mesh: runs EVERY plugin. system
	//                          plugins run as root; current_user plugins
	//                          (skill-protector) run via the platform's
	//                          runtime privilege-drop to the console user.
	//   daemon install       → User mesh: degraded fallback. Runs ONLY
	//                          current_user plugins; system plugins are
	//                          reported unavailable (reinstall with sudo
	//                          for full coverage).
	//
	// We never install both, and we never silently downgrade system→user
	// (see the fail-fast handling on osadapter.Install below).
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
	// Pin the desired platform version BEFORE calling Install so the
	// launchd mesh comes up with version.json already in place. If we
	// wrote it AFTER osadapter.Install, the first reconcile tick can
	// race in, observe no desired, and log a spurious "Blocked" — fine
	// in steady state but cosmetically alarming on first install. Go-
	// review HIGH #3.
	st := &core.Store{Dir: spec.Workdir}
	if err := st.WriteDesired(*desired); err != nil {
		fmt.Fprintln(os.Stderr, "write desired:", err)
		return 1
	}
	if err := osadapter.Install(spec); err != nil {
		// Fail fast, no silent downgrade (FEATURE 08 / ADR-0010). If the
		// operator clearly intended the full (system) install — they ran
		// with sudo, so euid is root — and it failed, we exit non-zero and
		// tell them how to choose the degraded user install EXPLICITLY. We
		// do NOT auto-retry as user: switching is the operator's decision.
		fmt.Fprintln(os.Stderr, "install:", err)
		for _, line := range installFailureHint(m, *desired) {
			fmt.Fprintln(os.Stderr, line)
		}
		return 1
	}
	fmt.Printf("installed %s mesh (desired platform = %s)\n", m, *desired)
	for _, line := range installCoverageNotice(m) {
		fmt.Println(line)
	}
	return 0
}

// installFailureHint returns the operator guidance printed when
// osadapter.Install fails. Only a failed SYSTEM install gets the
// "re-run without sudo for the degraded user install" hint — we never
// auto-downgrade; switching is an explicit operator choice. User/Test
// installs get no extra hint (nil). Pure + tested.
func installFailureHint(m mode.Mode, desired string) []string {
	if m != mode.System {
		return nil
	}
	return []string{
		"install: full (system) install failed; NOT falling back to the limited user install.",
		"install: to install the degraded user-only mode explicitly, re-run WITHOUT sudo:",
		"install:   daemon install -v " + desired + "   (user mode: only the skill protector runs)",
	}
}

// installCoverageNotice returns the honest coverage notice printed after
// a successful install. A USER install is the deliberate degraded
// fallback, so we say the system-level protections are unavailable and
// how to get them. System/Test installs get no notice (nil). Pure + tested.
func installCoverageNotice(m mode.Mode) []string {
	if m != mode.User {
		return nil
	}
	return []string{
		"note: user-mode install — only the Claude skill protector runs.",
		"note: site/game/packet protections are UNAVAILABLE; reinstall with sudo for full coverage.",
	}
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
