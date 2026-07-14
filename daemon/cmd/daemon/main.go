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
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/fetch"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/osadapter"
	"github.com/eliteGoblin/focusd/daemon/internal/platdir"
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
		// PROD launchd start (FEATURE 19): the minimized plist's
		// ProgramArguments is the binary alone — the role/mesh marker rides in
		// the plist's EnvironmentVariables (off-argv, hidden from `ps`).
		// Reconstruct the legacy subcommand argv from that env var so every
		// downstream path (parse/loop/doEnsure) sees exactly the argv it always
		// did. A missing or malformed var yields nil → fall through to usage()
		// unchanged (a human `daemon <cmd>` always passes non-empty argv and so
		// never reaches this branch).
		if synth := osadapter.ArgvFromEnv(); len(synth) > 0 {
			args = synth
		} else {
			usage()
			return 2
		}
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
	case "watchdog":
		return doWatchdog(args[1:])
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
	fmt.Fprintln(os.Stderr, "usage: daemon run|once|update|version|install|uninstall|watchdog|self-update|status [flags]")
}

type opts struct {
	// workdir is the DAEMON-HOME (FEATURE 21 / HF1): the daemon binary +
	// daemon-owned state (version.json, good, bad/, .roster, daemon.log). It
	// survives a platform-workdir wipe. Derived for a mesh role from the
	// binary's parent dir; explicit via --workdir for CLI/test.
	workdir string
	// platformWorkdir is the disposable platform-workdir (bin/<v>/platform,
	// plugins, state.db, platform.log), resolved from the pointer file in
	// daemon-home (see platdir.Resolve). Empty ⇒ legacy single-root: the
	// platform state lives under workdir (unit/e2e tests, non-mesh runs, and
	// the non-ticking `daemon update`).
	platformWorkdir string
	interval        time.Duration
	github          string
	asset           string
	releaseDir      string
	healthy         time.Duration
	unhealthy       time.Duration
	role            string
	testMode        bool
	mesh            bool
	roster          []string
}

func (o opts) spec(self string) osadapter.Spec {
	return osadapter.Spec{
		Mode: o.modeVal(), SelfPath: self, Workdir: o.workdir,
		Github: o.github, Asset: o.asset, Interval: o.interval,
		Roster: o.roster,
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
	// FEATURE 10 / ADR-0014: the WORKER in-process reconcile ticker now
	// defaults to the fast ~2s self-heal cadence so a single manual mesh
	// removal loses the whack-a-mole race (acceptance #2). This is the
	// LIVE A/B heal loop. It is DECOUPLED from the ensurer's launchd
	// StartInterval (still ~10s — launchd floors small StartInterval
	// values, so a 2s StartInterval there would be futile).
	iv := fs.Duration("interval", workerHealInterval, "worker reconcile interval (fast in-process self-heal)")
	gh := fs.String("github", defaultGithubRepo, "owner/repo for releases")
	// --asset is accepted ONLY for backward-compatibility with already-baked
	// plists (their argv still carries it); its value is IGNORED. The platform
	// asset is DERIVED (platformAsset) so a stale/wrong baked value — the old
	// self-heal bug — can never break the fetch again. Keeping the flag defined
	// also stops flag.Parse from choking on it and dropping later args
	// (--roster/--mesh/--r) on an existing install's restart.
	_ = fs.String("asset", "", "(deprecated, ignored) platform asset is derived from os/arch")
	rd := fs.String("release-dir", "", "use a local fake release dir instead of GitHub")
	hd := fs.Duration("healthy", 5*time.Second, "alive longer than this ⇒ promote good")
	ud := fs.Duration("unhealthy", 3*time.Second, "exit sooner than this ⇒ crashed")
	rl := fs.String("r", "a", "mesh role: a|b")
	tm := fs.String("test-mode-flag", "false", "use test-mode launchd labels")
	mesh := fs.Bool("mesh", false, "self-heal the launchd mesh (set only by the installer)")
	// --roster is still ACCEPTED for backward-compatibility with OLD plists
	// (their argv still bakes the comma-joined 3-label roster). When present
	// it WINS — an old plist on a new binary keeps working unchanged. New
	// (FEATURE 14 / ADR-0018) plists omit it entirely: the roster lives only
	// in the masked workdir file. Keeping the flag DEFINED also stops
	// flag.Parse from choking on an old plist's --roster and dropping the
	// trailing --mesh/--r that follow it.
	rs := fs.String("roster", "", "(backward-compat) comma-joined 3-label mesh roster from old plists")
	// Track whether the caller explicitly passed --workdir so a mesh role
	// can prefer an explicit value over the os.Executable()-derived one.
	wdSet := false
	_ = fs.Parse(args)
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "workdir" {
			wdSet = true
		}
	})

	testMode := *tm == "true"
	roster := splitRoster(*rs)
	workdir := *wd

	// FEATURE 14 / ADR-0018: a mesh role (a `run … --mesh` worker or the
	// `ensure` subcommand) launched from a NEW minimized plist carries
	// neither --workdir nor --roster. Recover both off the command line:
	//   - workdir: explicit --workdir wins; else the parent dir of the
	//     daemon binary (it is relocated INSIDE the workdir); else the
	//     default. os.Executable() failure falls back to the default.
	//   - roster: an explicit --roster (old plist) wins; else, for a non-test
	//     mesh role, read it from the masked workdir file. A read error leaves
	//     the roster nil (Spec.Label's dev fallback applies; never crash).
	// CLI subcommands that run a binary OUTSIDE the workdir (update,
	// self-update, status, watchdog, install) do NOT call parse(), so this
	// derivation never touches their workdir logic.
	if isMeshRole(name, *mesh) {
		if !wdSet {
			workdir = deriveMeshWorkdir()
		}
		if roster == nil && !testMode {
			// Require the EXACT mesh size: core.ReadRoster validates non-empty
			// labels but not count, so a truncated/edited .roster could yield a
			// short slice and let Spec.Label backfill missing positions with dev
			// labels (corrupting mesh labels). A non-3 slice is treated as
			// unreadable → roster stays nil (Spec.Label dev fallback). (Copilot.)
			if labels, rerr := core.ReadRoster((&core.Store{Dir: workdir}).RosterPath()); rerr == nil && len(labels) == len(osadapter.AllRoles) {
				roster = labels
			}
		}
	}

	return opts{
		workdir: workdir, interval: *iv, github: *gh, asset: platformAsset(),
		releaseDir: *rd, healthy: *hd, unhealthy: *ud, role: *rl,
		testMode: testMode, mesh: *mesh, roster: roster,
	}
}

// isMeshRole reports whether a parsed invocation is a mesh member that
// must self-recover workdir + roster off-argv (FEATURE 14 / ADR-0018): a
// `run … --mesh` worker, or the `ensure` subcommand.
func isMeshRole(name string, mesh bool) bool {
	return name == "ensure" || mesh
}

// deriveMeshWorkdir resolves a mesh member's workdir from its own binary
// path (the disguised binary lives inside the workdir, FEATURE 14 /
// ADR-0018). Falls back to defaultWorkdir() when os.Executable() fails or
// yields no usable parent — never empty.
func deriveMeshWorkdir() string {
	self, err := os.Executable()
	if err != nil {
		return defaultWorkdir()
	}
	if wd := osadapter.WorkdirFromBinary(self); wd != "" {
		return wd
	}
	return defaultWorkdir()
}

// supportRootForDaemonHome returns the Application-Support root under which the
// disposable platform-workdir is (re)created for a mode (FEATURE 21 / HF1).
// user/system → the mode's real support root; test → the daemon-home's PARENT
// (the e2e sandbox) so a test platform-workdir stays a sibling of daemon-home
// inside the sandbox and a fresh recreate never escapes it.
func supportRootForDaemonHome(m mode.Mode, daemonHome string) string {
	if m != mode.Test {
		if home, err := os.UserHomeDir(); err == nil {
			return mode.SupportRoot(m, home)
		}
	}
	return filepath.Dir(daemonHome)
}

// resolvePlatformWorkdir resolves — and self-heals — the disposable
// platform-workdir for a daemon-home via the pointer file (FEATURE 21 / HF1).
// When the pointer is missing or its target was wiped, platdir.Resolve creates
// a FRESH platform-workdir and rewrites the pointer. A resolve failure is
// non-fatal: return "" so build() degrades to the legacy single-root (platform
// state under daemon-home) rather than refusing to run.
func resolvePlatformWorkdir(m mode.Mode, daemonHome string) string {
	pw, err := platdir.Resolve(daemonHome, supportRootForDaemonHome(m, daemonHome))
	if err != nil {
		return ""
	}
	return pw
}

// workerHealInterval is the fast in-process worker reconcile cadence
// (FEATURE 10 / ADR-0014, acceptance #2). The live A/B workers tick this
// fast so a single manual mesh removal is healed in ~2s — inside the ~5s
// a person needs for the next one-at-a-time removal. The ensurer's
// launchd StartInterval stays the slower osadapter.EnsureBackstopInterval.
const workerHealInterval = 2 * time.Second

// platformAsset is the protection-engine release asset name for THIS
// daemon's OS/arch. Releases are named platform-{GOOS}-{GOARCH}, so the
// name is FULLY DETERMINED — it is DERIVED, never an operator knob.
// A free-form asset flag was a self-heal footgun: a wrong/empty value
// (the daemon's own asset name, or "") silently 404'd the platform fetch,
// so the engine binary could never be re-fetched and had to be hand-placed
// — defeating the daemon's whole self-recovery purpose. KISS: derive,
// don't configure, so every rebuild path (install, self-update, watchdog)
// is correct by construction.
func platformAsset() string { return "platform-" + runtime.GOOS + "-" + runtime.GOARCH }

// defaultGithubRepo is the fixed product release repo. Single source of
// truth so the flag defaults and the watchdog's local rebuild (which has
// no operator argv to read) can never drift to an empty/different value —
// an empty repo would malform the fetch URL → 404 → no self-heal.
const defaultGithubRepo = "eliteGoblin/focusd"

// defaultPlatformVersion is the baked, compiled-in platform version the
// reconcile loop adopts when the on-disk store carries NO desired version
// (FEATURE 17, recovery resilience). A wiped workdir would otherwise leave
// the daemon Blocked forever (no desired ⇒ no platform ⇒ no protection); the
// baked fallback lets a survivor re-pin a known-good version and self-heal.
//
// FLOOR-not-ceiling: this is consulted ONLY when the store desired is empty.
// `install -v` / `update` always WriteDesired and win, so the fallback can
// never pin DOWN a newer pinned version. Bump it on each release so a
// fresh-wiped install self-heals to a current build. Validated by
// TestDefaultPlatformVersionValid against isValidVersionTag.
const defaultPlatformVersion = "v0.16.3"

// fixedSingletonLockName is the basename of the cross-generation platform
// singleton lock (FEATURE 17, Item 2). It lives at a FIXED path under the
// mode's Application Support root — NOT inside the rotating workdir — so the
// flock elects exactly ONE platform supervisor even across path-rotating
// self-update generations (a per-workdir lock would let each generation run
// its own platform). Disguised as a dotted Apple-metadata-looking file so a
// casual `ls` doesn't flag it; the durable commitment weight lives elsewhere.
const fixedSingletonLockName = ".com.apple.metadata.plist.lck"

// singletonLockPath returns the path of the cross-generation platform
// singleton lock for a mode. user/system → the FIXED mode-keyed path under
// SupportRoot (survives workdir rotation). test → the per-workdir path
// (Store.LockPath()) so concurrent e2e installs stay isolated.
func singletonLockPath(m mode.Mode, workdir string) string {
	if m == mode.Test {
		return (&core.Store{Dir: workdir}).LockPath()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// No resolvable home → mode.SupportRoot would yield a RELATIVE path, and
		// a relative singleton lock defeats the cross-generation election (each
		// generation, started from its own cwd, would flock a different file →
		// twin platforms). Fall back to the per-workdir lock: it still elects one
		// platform within a generation, which is strictly safer than a relative
		// path that elects none across them.
		return (&core.Store{Dir: workdir}).LockPath()
	}
	return filepath.Join(mode.SupportRoot(m, home), fixedSingletonLockName)
}

// splitRoster parses the comma-joined --roster flag into the label set.
// Empty → nil (the dev/test fallback in Spec.Label takes over).
func splitRoster(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func defaultWorkdir() string {
	h, _ := os.UserHomeDir()
	return h + "/Library/Application Support/focusd-daemon"
}

func build(o opts) (*core.Executor, *slog.Logger) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	// FEATURE 21 (HF1): the daemon's durable state lives under the daemon-home
	// (o.workdir); the platform's disposable binaries + process live under the
	// separate platform-workdir when one has been resolved (loop/install). An
	// empty platformWorkdir keeps the legacy single-root layout (unit/e2e
	// tests, non-mesh runs, and the non-ticking `daemon update`).
	platWD := o.platformWorkdir
	if platWD == "" {
		platWD = o.workdir
	}
	st := &core.Store{Dir: o.workdir}
	if platWD != o.workdir {
		st.PlatformDir = platWD
	}
	// HF4 (FEATURE 24): outside test mode, seed the per-install disguise salt so
	// the platform binary lands under a disguised basename and the child runs
	// under a generic argv[0] (no version/'platform'/workdir in `ps`). Test mode
	// deliberately stays on the legacy, deterministic layout so e2e is
	// self-contained. Best-effort: a write failure degrades to the legacy layout
	// (empty salt ⇒ PlatformArgv0/BinPath fall back) rather than blocking.
	if o.modeVal() != mode.Test {
		_, _ = st.EnsureInstallSalt()
	}
	var f core.Fetcher
	if o.releaseDir != "" {
		f = &fetch.Local{Dir: o.releaseDir}
	} else {
		f = &fetch.GitHub{Repo: o.github, Asset: o.asset}
	}
	p := platformsvc.New(platWD)
	// HF4: set the disguised argv[0] for the platform child (empty in test mode /
	// no-salt ⇒ ProcSvc keeps the legacy visible argv).
	p.Argv0 = st.PlatformArgv0()
	if o.healthy > 0 {
		p.Healthy = o.healthy
	}
	if o.unhealthy > 0 {
		p.Unhealthy = o.unhealthy
	}
	// Crash-safe singleton lock held by the daemon: only the reconcile loop
	// (loop()->Tick->apply) ever acquires it, electing one platform supervisor
	// across the A/B mesh roles. Non-ticking callers (update/install) construct
	// but never acquire. NewFileLock's zero value is unlocked.
	e := core.NewExecutor(st, f, p, core.NewFileLock(), log)
	// FEATURE 17 Item 1: bake the fallback platform version so a wiped workdir
	// self-heals instead of blocking. Guard with the strict tag validator —
	// an invalid baked value leaves Fallback empty (today's safe Blocked).
	if isValidVersionTag(defaultPlatformVersion) {
		e.Fallback = defaultPlatformVersion
	}
	// FEATURE 17 Item 2: elect one platform across path-rotating generations
	// via a fixed, mode-keyed lock path (test mode stays per-workdir).
	e.LockFilePath = singletonLockPath(o.modeVal(), o.workdir)
	// FEATURE 25 (Element 3): only a REAL installed mesh worker reaps orphaned
	// platform processes. Gated OFF for test mode (the reaper anchors on
	// mode.Resolve()'s SupportRoot, which for a HOME-overridden e2e sandbox is
	// safe, but there is no launchd-reparent orphan class to chase there) and for
	// non-mesh foreground runs (dev/e2e `daemon run`), so a stray foreground
	// daemon never scans/kills real user platforms. On non-darwin
	// osadapter.ReapForeignPlatforms is a no-op stub.
	//
	// HF4 (FEATURE 24) reconciliation: the reaper is NAMING-AGNOSTIC — it
	// classifies orphans by Ed25519 signature verification of the executable
	// under this mode's SupportRoot (not by the platform basename), so HF4's
	// disguised basename needs no coupling here.
	if o.mesh && o.modeVal() != mode.Test {
		e.ReapForeign = osadapter.ReapForeignPlatforms
	}
	return e, log
}

func loop(args []string, once bool) int {
	o := parse("run", args)
	// FEATURE 21 (HF1): resolve — and self-heal — the disposable
	// platform-workdir from the pointer in daemon-home BEFORE building the
	// executor. A wiped platform-workdir (pointer target gone) is re-created
	// fresh here, so the very next tick re-fetches + restarts the platform
	// while the daemon-home (binary + state) is untouched.
	o.platformWorkdir = resolvePlatformWorkdir(o.modeVal(), o.workdir)
	e, log := build(o)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// NOTE: we deliberately do NOT release the singleton lock early on
	// shutdown. The platform child is NOT stopped here (it persists for
	// protection continuity), and on a launchd bootout the kernel kills our
	// whole process group — so the platform dies as this process exits. The
	// fd-tied lock is freed by the kernel at process exit, which orders AFTER
	// that teardown — so a standby cannot acquire the lock and start a second
	// platform while ours is still alive. Releasing early (before exit) would
	// reopen exactly that duplicate-platform window. (Copilot review.)

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
			// FEATURE 18 / ADR-0020: out-of-band COMPANION mutual guarding — the
			// mesh's own reconcile keeps the companion rail up (idempotent) AND
			// refreshes the daemon heartbeat the companion watches, so a healthy
			// daemon is never falsely "recovered". Best-effort; never fails a
			// tick. (Supersedes the FEATURE 12 cron watchdog rail.)
			if desired := (&core.Store{Dir: o.workdir}).Desired(); desired != "" {
				if cerr := osadapter.EnsureCompanion(spec.Mode, self, desired); cerr != nil {
					log.Warn("ensure-companion", "err", cerr)
				}
			}
			if herr := osadapter.TouchCompanionHeartbeat(spec.Mode); herr != nil {
				log.Warn("touch-heartbeat", "err", herr)
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
	gh := fs.String("github", defaultGithubRepo, "owner/repo (for `update` with no version arg)")
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

	o := opts{workdir: workdir, github: *gh, asset: platformAsset()}
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
	gh := fs.String("github", defaultGithubRepo, "owner/repo")
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
		Github: *gh, Asset: platformAsset(),
		// FEATURE 10 / ADR-0014: the worker heal cadence is a FIXED ~2s
		// security constant (it closes the manual-removal whack-a-mole), baked
		// into the worker plists — NOT an operator knob (a stale --interval
		// must not be able to reopen the loophole). The ensurer's launchd
		// StartInterval stays the slower backstop, DECOUPLED from it.
		Interval:       workerHealInterval,
		EnsureInterval: osadapter.EnsureBackstopInterval,
	}
	if err := installMesh(self, &spec, *desired); err != nil {
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

	// FEATURE 18 / ADR-0020: stand up the out-of-band COMPANION rail AFTER the
	// mesh is up — a SEPARATE minimal binary in its OWN fixed disguised folder
	// (outside the daemon's workdir, so a workdir wipe leaves it) that recovers
	// the daemon OFFLINE from a signed backup, on launchd (no Full Disk Access),
	// SUPERSEDING the FEATURE 12 cron watchdog. Best-effort + skipped in Test:
	// a companion failure must NOT fail the install (the mesh is already up and
	// the mesh's own reconcile re-ensures the companion). `self` is the signed
	// installer binary used as the offline backup.
	if osSupportsLaunchd() && m != mode.Test {
		if cerr := osadapter.EnsureCompanion(m, self, *desired); cerr != nil {
			// Generic message only: cerr may embed the disguised companion path.
			fmt.Fprintln(os.Stderr, "companion: could not stand up out-of-band rail (best-effort)")
		}
	}
	return 0
}

// installMesh stands up the launchd mesh: relocate (user/system) → pin the
// desired platform version → osadapter.Install. Extracted from doInstall so
// the out-of-band watchdog (`daemon watchdog`) can run the SAME install path
// for a local rebuild (FEATURE 12 / ADR-0016) without re-deriving the
// relocate→WriteDesired→Install sequence. Mutates spec's SelfPath/Workdir/
// Roster in place for the user/system relocation.
func installMesh(self string, spec *osadapter.Spec, desired string) error {
	m := spec.Mode

	// FEATURE 21 (HF1): establish TWO roots with distinct lifetimes.
	//   - daemon-home: the daemon binary + daemon-owned state (version.json,
	//     good, bad/, .roster, daemon.log). The mesh plists point at the binary
	//     HERE, so deleting the platform-workdir cannot disable the daemon.
	//   - platform-workdir: the disposable engine storage (bin/<v>/platform,
	//     plugins, state.db, platform.log), recorded via a pointer file in
	//     daemon-home. spec.Workdir carries the DAEMON-HOME (plist daemon.log +
	//     the store both key off it); the platform-workdir is resolved at
	//     runtime from the pointer (see resolvePlatformWorkdir).
	var daemonHome, supportRoot string
	if m == mode.Test {
		// e2e sandbox: daemon-home + platform-workdir are SIBLINGS under the
		// caller-supplied workdir, so deleting one leaves the other and both
		// tear down with the sandbox. The binary is NOT relocated (test bakes an
		// explicit --workdir), so daemon-home is a fixed sandbox subdir.
		supportRoot = spec.Workdir
		daemonHome = filepath.Join(spec.Workdir, "daemon-home")
		if err := os.MkdirAll(daemonHome, 0o700); err != nil {
			return fmt.Errorf("mkdir daemon-home: %w", err)
		}
		spec.Workdir = daemonHome
	} else {
		// user/system: relocate the daemon binary into a hidden daemon-home
		// under this mode's Application Support root (user → ~/Library, system →
		// /Library) + three INDEPENDENT disguised mesh labels (FEATURE 10 /
		// ADR-0014: distinct vendor families, no shared base, no role token).
		home, _ := os.UserHomeDir()
		supportRoot = mode.SupportRoot(m, home)
		daemonHome = relocate.HiddenWorkdir(supportRoot)
		reloc, rerr := relocate.RelocateInto(self, daemonHome)
		if rerr != nil {
			return fmt.Errorf("relocate: %w", rerr)
		}
		spec.SelfPath = reloc
		spec.Workdir = daemonHome
		spec.Roster = relocate.GenerateRoster()
		// Do NOT print the disguised daemon-home PATH OR the roster labels —
		// both are strings a weak-moment self needs for a targeted bootout / rm.
		// Print only the relocation FACT + mode, never the path. (This line also
		// runs in the watchdog rebuild context.)
		fmt.Printf("relocated (mode %s)\n", m)
	}

	// Create the disposable platform-workdir (a SEPARATE hidden dir, sentinel-
	// marked) and record it in daemon-home's pointer file, so the first launch
	// resolves it — and a later `rm -rf` of it is re-created fresh from the
	// pointer while daemon-home stays intact.
	platformWorkdir, perr := platdir.Create(supportRoot)
	if perr != nil {
		return fmt.Errorf("create platform-workdir: %w", perr)
	}
	if err := platdir.Write(daemonHome, platformWorkdir); err != nil {
		return fmt.Errorf("write platform pointer: %w", err)
	}

	// Pin the desired platform version into the DAEMON-HOME store BEFORE calling
	// Install so the launchd mesh comes up with version.json already in place.
	// If we wrote it AFTER osadapter.Install, the first reconcile tick can race
	// in, observe no desired, and log a spurious "Blocked". Go-review HIGH #3.
	st := &core.Store{Dir: daemonHome}
	if err := st.WriteDesired(desired); err != nil {
		return fmt.Errorf("write desired: %w", err)
	}
	if err := osadapter.Install(*spec); err != nil {
		return fmt.Errorf("install mesh: %w", err)
	}
	// FEATURE 25: the new generation is up FIRST (above); now CONVERGE to exactly
	// one daemon + one platform across BOTH domains. This supersedes the FEATURE
	// 17/21 mode-scoped retire + sweep calls (which only ever touched spec.Mode,
	// so a sudo install never retired the user generations and vice-versa).
	// ConvergeSingleInstance retires every OTHER generation in the user AND system
	// domains, kills each retired generation's platform, reaps orphaned platform
	// processes, and sweeps stale daemon-home + platform-workdirs. Best-effort: a
	// convergence failure must NEVER fail an otherwise-successful install, and the
	// retired/reaped labels/paths are never surfaced (counts only). The survivor
	// platform is not running yet (the first tick starts it), so keepPlatformPID
	// is 0 — the survivor is exempted by its (path) binary instead.
	keepPlatformBin := (&core.Store{Dir: daemonHome, PlatformDir: platformWorkdir}).BinPath(desired)
	if retired, reaped, cerr := osadapter.ConvergeSingleInstance(spec.Mode, spec.SelfPath, keepPlatformBin, 0); cerr != nil {
		// Generic message only: cerr could embed a disguised path.
		fmt.Fprintln(os.Stderr, "install: converge single-instance (best-effort) failed")
	} else {
		if retired > 0 {
			fmt.Printf("retired %d prior generation(s)\n", retired)
		}
		if reaped > 0 {
			fmt.Printf("reaped %d orphan platform process(es)\n", reaped)
		}
	}
	return nil
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
	// FEATURE 18 / ADR-0020: tear down the out-of-band COMPANION rail (its
	// launchd job + folder). Best-effort — a leftover companion would rebuild
	// the mesh AFTER a deliberate, gate-satisfied uninstall, which is wrong, so
	// we try; but a failure here must not fail the (already-completed) mesh
	// teardown. Generic message only (the error may embed the disguised folder).
	if cerr := osadapter.RemoveCompanion(mode.Resolve()); cerr != nil {
		fmt.Fprintln(os.Stderr, "uninstall: remove companion rail (best-effort) failed")
	}
	// Migration: also strip the LEGACY FEATURE 12 cron watchdog if an older
	// install left one behind (superseded by the companion). Best-effort.
	if werr := osadapter.RemoveWatchdog(mode.Resolve()); werr != nil {
		fmt.Fprintln(os.Stderr, "uninstall: remove legacy watchdog (best-effort) failed")
	}
	_ = uninstallgate.Clear(gpath) // gate state dies with the install
	// Redact the disguised labels (consistent with install/self-update): the
	// removed set is exactly the strings a targeted bootout would need. Count
	// only. (go-reviewer L4 / security-reviewer MEDIUM.)
	fmt.Printf("uninstalled (prod): %d entries removed\n", len(removed))
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
