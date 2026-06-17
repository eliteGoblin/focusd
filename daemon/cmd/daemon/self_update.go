package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/fetch"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/osadapter"
	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// daemonAssetFetcher is the verified-download seam used by self-update.
// Both fetch.GitHub and fetch.Local satisfy it; the CLI picks one
// based on whether --release-dir is set.
type daemonAssetFetcher interface {
	DownloadVerified(ctx context.Context, tag, asset, dstPath string) error
}

// selfUpdateOpts is what doSelfUpdate parses out of argv. Extracted so
// the CLI test can build it directly and exercise runSelfUpdate
// without round-tripping through flag.NewFlagSet.
type selfUpdateOpts struct {
	tag            string
	workdir        string
	github         string
	assetPattern   string
	releaseDir     string
	dryRun         bool
	keepOld        bool
	healthyTimeout time.Duration
	probeInterval  time.Duration
}

// doSelfUpdate is the CLI dispatch for `daemon self-update`. macOS-
// only (launchd lifecycle); Linux/Windows reach the same gate.
//
// Surface (architect-approved):
//
//	daemon self-update <daemon-tag> [--workdir D] [--github owner/repo]
//	  [--asset-pattern PAT] [--release-dir D] [--dry-run] [--keep-old]
//	  [--healthy-timeout 15s] [--probe-interval 1s]
//
// The tag must match `^daemon-v\d+\.\d+\.\d+(-…)?$` — anything else
// is rejected upfront (same path-traversal concern as `daemon update`).
func doSelfUpdate(args []string) int {
	if !osSupportsLaunchd() {
		fmt.Fprintln(os.Stderr, "self-update: unsupported on", runtime.GOOS, "(darwin/launchd only)")
		return 1
	}
	o, code := parseSelfUpdate(args)
	if code != 0 {
		return code
	}
	return runSelfUpdate(o)
}

func parseSelfUpdate(args []string) (selfUpdateOpts, int) {
	fs := flag.NewFlagSet("self-update", flag.ContinueOnError)
	wd := fs.String("workdir", "", "override discovered workdir")
	gh := fs.String("github", defaultGithubRepo, "owner/repo for releases")
	ap := fs.String("asset-pattern", "daemon-darwin-{arch}",
		"asset name template — {arch} substituted with runtime.GOARCH")
	rd := fs.String("release-dir", "", "use a local fake release dir instead of GitHub (testing)")
	dr := fs.Bool("dry-run", false, "fetch + verify + render, don't bootstrap; print intended ops")
	ko := fs.Bool("keep-old", false, "leave old plists + binary on disk after success")
	ht := fs.Duration("healthy-timeout", 15*time.Second, "post-bootstrap health poll window")
	pi := fs.Duration("probe-interval", 1*time.Second, "health poll cadence")
	if err := fs.Parse(args); err != nil {
		// flag already printed; return non-zero so caller exits.
		return selfUpdateOpts{}, 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr,
			"self-update: exactly one positional argument required (daemon-vX.Y.Z)")
		fs.Usage()
		return selfUpdateOpts{}, 2
	}
	tag := fs.Arg(0)
	if !isValidDaemonTag(tag) {
		fmt.Fprintln(os.Stderr,
			"self-update: tag must match `daemon-vX.Y.Z` (got:", tag+")")
		return selfUpdateOpts{}, 2
	}
	// Security-reviewer MEDIUM #2: --github is interpolated into the
	// GH API URL `repos/<owner>/<repo>/releases/tags/<tag>`. Reject
	// anything that's not a plain `owner/repo` to defeat URL-fragment
	// injection. (Public-key trust still gates the asset, but tightening
	// here gives a clear error instead of a confusing 404.)
	if !isValidGithubRepo(*gh) {
		fmt.Fprintln(os.Stderr,
			"self-update: --github must be `owner/repo` (got:", *gh+")")
		return selfUpdateOpts{}, 2
	}
	return selfUpdateOpts{
		tag: tag, workdir: *wd, github: *gh, assetPattern: *ap,
		releaseDir: *rd, dryRun: *dr, keepOld: *ko,
		healthyTimeout: *ht, probeInterval: *pi,
	}, 0
}

func runSelfUpdate(o selfUpdateOpts) int {
	// A. Pre-flight: find the current install at the invocation's
	// mode (sudo → system, else user). Reject if the daemon was
	// installed with the OTHER mode — the operator must invoke with
	// the matching privilege.
	invokeMode := mode.Resolve()
	cur, err := osadapter.FindCurrentInstall(invokeMode, sig.VerifyFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "self-update: discover install:", err)
		return 1
	}
	if cur.BinaryPath == "" {
		// Maybe it's installed under the OTHER mode — give a clear hint.
		other := mode.System
		if invokeMode == mode.System {
			other = mode.User
		}
		fmt.Fprintf(os.Stderr,
			"self-update: no %s-mode install found in %s.\n"+
				"  Hint: try %s mode (look in %s); %s-mode requires %s.\n",
			invokeMode, otherDirHint(invokeMode),
			other, otherDirHint(other),
			other, sudoHintFor(other))
		return 1
	}

	// Recover workdir: prefer the value baked into the install's plist
	// argv (authoritative). --workdir is accepted ONLY as a fallback
	// when discovery couldn't recover it — never as an override.
	// Copilot #5: allowing override risked silent workdir migration
	// that strands state.db / version.json / bin/v* in the old dir.
	workdir := cur.Workdir
	if workdir == "" {
		if o.workdir == "" {
			fmt.Fprintln(os.Stderr, "self-update: could not recover --workdir from install; pass --workdir")
			return 1
		}
		workdir = o.workdir
	} else if o.workdir != "" && o.workdir != workdir {
		fmt.Fprintf(os.Stderr,
			"self-update: --workdir=%q disagrees with discovered install workdir=%q; "+
				"refusing to migrate workdir (omit --workdir to use the discovered one)\n",
			o.workdir, workdir)
		return 1
	}

	// Resolve asset name: {arch} → runtime.GOARCH. Keep this dumb;
	// we accept any asset pattern the caller provides because the
	// signature trailer is what actually gates trust.
	asset := strings.ReplaceAll(o.assetPattern, "{arch}", runtime.GOARCH)

	// B. Download + verify into a temp file inside the workdir (so
	// the os.Rename in step C is on the same filesystem).
	tmpDir := filepath.Join(workdir, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "self-update: mkdir tmp:", err)
		return 1
	}
	tmpDL := filepath.Join(tmpDir, "daemon-dl-"+relocate.RandomBinaryName())
	defer os.Remove(tmpDL)

	var f daemonAssetFetcher
	if o.releaseDir != "" {
		f = &fetch.Local{Dir: o.releaseDir}
	} else {
		f = &fetch.GitHub{Repo: o.github, Asset: asset}
	}
	ctx := context.Background()
	if err := f.DownloadVerified(ctx, o.tag, asset, tmpDL); err != nil {
		fmt.Fprintln(os.Stderr, "self-update: download:", err)
		return 1
	}
	newBin, rerr := os.ReadFile(tmpDL)
	if rerr != nil {
		fmt.Fprintln(os.Stderr, "self-update: read verified bytes:", rerr)
		return 1
	}

	// Build the new Spec: rotated SelfPath + a NEW independent-label
	// roster; same workdir. Reusing osadapter.Spec.Label downstream, the
	// new roster (FEATURE 10 / ADR-0014: distinct vendor families, no
	// shared base, no role token) is the disguised label set that lets the
	// old and new meshes coexist during the swap.
	newPath := filepath.Join(workdir, relocate.RandomBinaryName())
	if newPath == cur.BinaryPath {
		// Defensive: 4-hex collision is astronomical but explicit
		// safety is cheaper than a silent AMFI block.
		fmt.Fprintln(os.Stderr, "self-update: rotated path matches current (try again)")
		return 1
	}
	newRoster := relocate.GenerateRoster()
	// FEATURE 10 / ADR-0014: the worker heal cadence is a ~2s SECURITY
	// constant (it closes the manual-removal whack-a-mole loophole), NOT an
	// operator preference. FORCE it on every self-update so (a) migrating an
	// OLD pre-F10 mesh that ran at 10s upgrades to the fast heal, and (b) a
	// tuned/stale --interval can't carry a slow cadence forward and reopen
	// the gap. (Supersedes the earlier "preserve operator interval" note —
	// that predated the security framing.)
	interval := workerHealInterval
	newSpec := osadapter.Spec{
		Mode:     invokeMode,
		SelfPath: newPath,
		Workdir:  workdir,
		Github:   o.github,
		// The PLATFORM asset is derived (platform-{os}-{arch}), NOT the
		// daemon asset `asset` used above to download the daemon binary.
		// Baking the daemon asset here was the self-heal bug: the rebuilt
		// mesh fetched a non-existent platform asset → 404 → no recovery.
		Asset:    platformAsset(),
		Interval: interval,
		// Keep the ensurer's launchd StartInterval the slower backstop,
		// decoupled from the fast worker --interval (FEATURE 10 / ADR-0014).
		EnsureInterval: osadapter.EnsureBackstopInterval,
		Roster:         newRoster,
	}

	if o.dryRun {
		printDryRun(cur, newSpec, o)
		return 0
	}

	if err := osadapter.SelfUpdateProd(cur, newSpec, newBin,
		o.healthyTimeout, o.probeInterval, o.keepOld); err != nil {
		fmt.Fprintln(os.Stderr, "self-update:", err)
		return 1
	}
	// FEATURE 12 / ADR-0016: keep the out-of-band watchdog copy in sync with
	// the rotated binary — place a fresh copy of the new binary + rewrite the
	// cron line to it (and the current desired). Best-effort; do NOT print
	// paths, and a refresh failure must not fail the (completed) self-update.
	desired := (&core.Store{Dir: workdir}).Desired()
	if werr := osadapter.RefreshWatchdog(invokeMode, newPath, desired); werr != nil {
		fmt.Fprintln(os.Stderr, "self-update: refresh watchdog (best-effort)")
	}
	// Do NOT print the disguised roster labels — they are exactly the
	// strings a targeted bootout needs (FEATURE 10 honest-limitations).
	fmt.Printf("self-update ok: %s → %s\n", cur.BinaryPath, newPath)
	return 0
}

// printDryRun emits the intended operations for the operator to
// review without performing any launchctl or filesystem mutations.
func printDryRun(cur osadapter.CurInstall, newSpec osadapter.Spec, o selfUpdateOpts) {
	w := io.Writer(os.Stdout)
	fmt.Fprintln(w, "self-update --dry-run")
	fmt.Fprintln(w, "  tag           ", o.tag)
	fmt.Fprintln(w, "  current binary", cur.BinaryPath)
	fmt.Fprintln(w, "  new binary    ", newSpec.SelfPath)
	// The disguised roster labels (old + new) are deliberately NOT printed
	// — they are the strings a targeted bootout needs (FEATURE 10 /
	// ADR-0014). We show only the count so the operator sees the mesh size
	// is right without learning the names.
	fmt.Fprintln(w, "  current mesh  ", len(cur.Labels), "labels (redacted)")
	fmt.Fprintln(w, "  new mesh      ", len(osadapter.AllRoles), "labels (redacted)")
	fmt.Fprintln(w, "  workdir       ", newSpec.Workdir, "(unchanged)")
	fmt.Fprintln(w, "  --keep-old    ", o.keepOld)
	fmt.Fprintln(w, "  health timeout", o.healthyTimeout)
	fmt.Fprintln(w, "  probe interval", o.probeInterval)
	fmt.Fprintln(w, "intended ops (in order):")
	fmt.Fprintln(w, "  C. place new binary at", newSpec.SelfPath)
	fmt.Fprintln(w, "  D. write 3 new plists at new label filenames")
	fmt.Fprintln(w, "  E. bootstrap new A, B, ensure")
	fmt.Fprintln(w, "  F. health-poll (2 consecutive ok required)")
	fmt.Fprintln(w, "  G. bootout OLD in REVERSE order (ensure → B → A)")
	fmt.Fprintln(w, "  H. rm old plists + old binary", "(skipped if --keep-old)")
}

// otherDirHint returns the LaunchDir the operator should be looking
// at for the OTHER mode — used in the "no install found" error path
// to point at the alternative install location.
func otherDirHint(m mode.Mode) string {
	home, _ := os.UserHomeDir()
	return mode.LaunchDir(m, home)
}

// sudoHintFor names the privilege needed to manage a given install
// mode — used in the "no install found" error message so the operator
// knows whether to re-invoke with or without sudo.
func sudoHintFor(m mode.Mode) string {
	if m == mode.System {
		return "sudo"
	}
	return "no sudo"
}
