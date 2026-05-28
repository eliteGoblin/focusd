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

	"github.com/eliteGoblin/focusd/daemon/internal/fetch"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/osadapter"
	"github.com/eliteGoblin/focusd/daemon/internal/relocate"
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
	tag             string
	workdir         string
	github          string
	assetPattern    string
	releaseDir      string
	dryRun          bool
	keepOld         bool
	healthyTimeout  time.Duration
	probeInterval   time.Duration
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
	gh := fs.String("github", "eliteGoblin/focusd", "owner/repo for releases")
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
	cur, err := osadapter.FindCurrentInstall(invokeMode)
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
				"  Hint: a %s-mode install is removed with sudo; a user-mode install without.\n",
			invokeMode, otherDirHint(invokeMode), other)
		return 1
	}

	// Recover workdir: explicit --workdir overrides; otherwise we
	// trust the value baked into the install's plist argv.
	workdir := cur.Workdir
	if o.workdir != "" {
		workdir = o.workdir
	}
	if workdir == "" {
		fmt.Fprintln(os.Stderr, "self-update: could not recover --workdir from install; pass --workdir")
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

	// Build the new Spec: rotated SelfPath + new Base; same workdir.
	// Preserve install-mode + non-disguised dev fallback handling by
	// reusing osadapter.Spec.Label downstream — the new Base is the
	// disguised label namespace that lets old/new plists coexist
	// during the swap.
	newPath := filepath.Join(workdir, relocate.RandomBinaryName())
	if newPath == cur.BinaryPath {
		// Defensive: 4-hex collision is astronomical but explicit
		// safety is cheaper than a silent AMFI block.
		fmt.Fprintln(os.Stderr, "self-update: rotated path matches current (try again)")
		return 1
	}
	newBase := relocate.RandomBase()
	newSpec := osadapter.Spec{
		Mode:     invokeMode,
		SelfPath: newPath,
		Workdir:  workdir,
		Github:   o.github,
		Asset:    asset,
		Interval: defaultInterval,
		Base:     newBase,
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
	fmt.Printf("self-update ok: %s → %s (new base=%s)\n", cur.BinaryPath, newPath, newBase)
	return 0
}

// printDryRun emits the intended operations for the operator to
// review without performing any launchctl or filesystem mutations.
func printDryRun(cur osadapter.CurInstall, newSpec osadapter.Spec, o selfUpdateOpts) {
	w := io.Writer(os.Stdout)
	fmt.Fprintln(w, "self-update --dry-run")
	fmt.Fprintln(w, "  tag           ", o.tag)
	fmt.Fprintln(w, "  current binary", cur.BinaryPath)
	fmt.Fprintln(w, "  current base  ", cur.Base)
	fmt.Fprintln(w, "  current labels", strings.Join(cur.Labels, ", "))
	fmt.Fprintln(w, "  new binary    ", newSpec.SelfPath)
	fmt.Fprintln(w, "  new base      ", newSpec.Base)
	fmt.Fprintln(w, "  new labels    ",
		newSpec.Label(osadapter.RoleA), newSpec.Label(osadapter.RoleB), newSpec.Label(osadapter.RoleEnsure))
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

// defaultInterval is the reconcile interval baked into self-update's
// new plists. Matches the rest of the daemon CLI's default; kept as a
// named constant rather than a CLI flag because operators rarely need
// to retune it and the install-time value is already baked into the
// old plists this code is replacing.
const defaultInterval = 10 * time.Second
