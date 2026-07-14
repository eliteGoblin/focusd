//go:build darwin

package osadapter

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// FEATURE 25 — continuous single-platform convergence (the DOMINANT reap).
//
// The daemon flock ELECTS one platform supervisor but never REAPS extras: on a
// daemon death the platform child reparents to launchd and SURVIVES; a standby
// daemon then acquires the freed flock and starts a SECOND platform. Every
// crash/self-update cycle adds one, so the machine accretes orphaned platforms.
// ReapForeignPlatforms is the reaper the flock lacked: the lock WINNER
// enumerates running platform processes and SIGTERM→SIGKILLs every one that is
// NOT the survivor it exempts.
//
// IDENTITY — signature-first, naming-agnostic (HF4 reconciliation)
//
// The original F25 reaper classified a platform by the greppable executable-path
// signature `/bin/<semver>/platform` under this mode's SupportRoot. HF4 (FEATURE
// 24) then DISGUISED the platform binary — a per-install-salt-derived basename
// with no 'platform'/version token — so that path signature matches nothing and
// the reaper would silently reap zero (fail-OPEN on the no-orphan guarantee).
//
// Re-coupling the classifier to HF4's disguised naming would just re-break the
// next time the layout changes. Instead the reaper now mirrors the two-tier
// pattern DiscoverAllGenerations already uses (verify → ENOENT fallback):
//
//  1. ANCHOR — the candidate's executable path must be strictly under THIS
//     mode's SupportRoot AND under a `.../bin/...` PLATFORM-layout segment. This
//     bounds the set of binaries we ever read/verify to focusd's own platform
//     tree; a process outside it is never a candidate. The bin-segment half is
//     the DAEMON/PLATFORM discriminator (FEATURE 25, C3): the platform binary is
//     <platform-workdir>/bin/<base> while the daemon mesh workers sit directly in
//     daemon-home (no bin segment). Both are signed focusd binaries under the same
//     SupportRoot, so without this the reaper could not tell a platform from a
//     daemon worker and would SIGKILL the mesh on a signed install (see
//     underBinSegment). The candidate's exec path is resolved kernel-first via
//     libproc proc_pidpath (disguise-proof) with lsof only as a fallback.
//
//  2. SIGNATURE tier (primary) — for a candidate under the root, sig.VerifyFile
//     the executable. A VALID Ed25519 signature ⇒ a genuine focusd platform ⇒
//     reap (unless it is the exempt survivor). This DROPS the basename
//     requirement entirely: it survives ANY platform-basename disguise, and a
//     decoy cannot forge validly-signed bytes (spoof-proof). A present-but-
//     UNSIGNED binary (verify ⇒ false, no error) is a MISMATCH — left alone.
//
//  3. DELETED-BINARY fallback (narrowed) — the ONLY case where crypto is
//     impossible: the workdir-rm'd orphan whose binary is gone but whose kernel
//     argv0 persists. When (and only when) sig.VerifyFile returns fs.ErrNotExist
//     we fall back to the legacy argv0 path suffix `/bin/<semver>/platform`,
//     still anchored under the root. A signature MISMATCH must NEVER trigger the
//     fallback — only ENOENT does — so a present-but-unsigned binary is never
//     reaped by the fallback. This is symmetric with DiscoverAllGenerations'
//     DeadGeneration/ENOENT tier.
//
// SAFETY: the survivor is exempt by PID (the daemon's own child, checked before
// any verify) AND by path; only the lock winner reaps; the SupportRoot anchor is
// mandatory (an empty/relative root reaps NOTHING). The reaper returns a COUNT
// only and never logs a matched path.
//
// platformSignatureRE is `$`-anchored to the END of the executable path so a
// sibling binary in the same versioned dir (e.g. `.../bin/v1.2.3/platform-debug`)
// can NEVER be misclassified — the match is the whole basename `platform`, not a
// prefix of it. It is used ONLY by the deleted-binary fallback now (the primary
// tier is signature-based and needs no naming assumption).
var platformSignatureRE = regexp.MustCompile(
	`/bin/v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.\-]+)?(\+[0-9A-Za-z.\-]+)?/platform$`)

// rawProc is one enumerated process: its pid and its kernel argv0 (`ps comm=`).
// comm is NOT the full command line — it is argv0 only — so a later ARGUMENT can
// never enter the deleted-binary suffix match. Under HF4 disguise argv0 is a bare
// generic token (no path); the anchored executable path used by the primary
// (signature) tier comes from resolveExecs, not from comm.
type rawProc struct {
	pid int
	cmd string
}

// procLister enumerates running processes as (pid, argv0). Real impl shells to
// `ps`; tests inject a fake table.
type procLister func() ([]rawProc, error)

// execResolver maps the enumerated procs to each pid's main executable path.
// Real impl (resolvePlatformExecs) proc_pidpath's the SAME procs the reaper
// already listed — no second `ps` — with lsof as a fallback; tests inject a fake
// map. macOS `ps comm=` reports argv0 (a bare token under HF4 disguise), NOT the
// executable path, so the anchored+verifiable path must come from libproc/lsof.
type execResolver func(procs []rawProc) (map[int]string, error)

// procKiller SIGTERM→SIGKILLs a pid. Real impl signals the process; tests record
// the pids asked to die.
type procKiller func(pid int)

// ReapForeignPlatforms SIGTERM→SIGKILLs every running platform process anchored
// under THIS mode's Application-Support root that is NOT keepPID. It is the
// exported entry the reconcile-loop winner and self-update wire to. Count-only
// return; the matched path is never surfaced. keepPID<=0 means "no PID
// exemption" (used at install time before any survivor is running); the reaper
// is still structurally incapable of reaching zero live platforms because the
// daemon that calls it always keeps + restarts its own survivor.
func ReapForeignPlatforms(keepPID int) (killed int, err error) {
	home, _ := os.UserHomeDir()
	root := mode.SupportRoot(mode.Resolve(), home)
	return reapForeignPlatforms(root, keepPID, "", listPlatformProcs, resolvePlatformExecs, sig.VerifyFile, killProc)
}

// reapForeignPlatforms is the pure, seam-injected core. supportRoot is the
// anchor (a foreign platform MUST live strictly under it); keepPID and keepPath
// are the twin exemptions for the survivor (path covers the window where the
// survivor PID is not yet known, e.g. at install/convergence time). verify is the
// signature seam (production: sig.VerifyFile).
func reapForeignPlatforms(
	supportRoot string, keepPID int, keepPath string,
	list procLister, resolveExecs execResolver, verify Verifier, kill procKiller,
) (int, error) {
	// A non-absolute / empty root cannot anchor the signature safely — refuse to
	// reap ANYTHING rather than risk an unanchored match. (Mirrors the
	// safeToRemoveWorkdir belt: no anchor ⇒ no action.)
	if supportRoot == "" || !filepath.IsAbs(supportRoot) {
		return 0, nil
	}
	procs, err := list()
	if err != nil {
		return 0, err
	}
	// Best-effort exec-path map over the procs we just listed (libproc, lsof
	// fallback). A resolve failure yields an empty map: the signature tier then
	// finds no under-root path and only the deleted-binary fallback (kernel argv0)
	// can still fire — fail-safe, never fail-open into over-matching.
	execByPID, _ := resolveExecs(procs)
	keepClean := ""
	if keepPath != "" {
		keepClean = filepath.Clean(keepPath)
	}
	killed := 0
	for _, p := range procs {
		if p.pid <= 0 || p.pid == keepPID {
			continue // survivor exempt by PID BEFORE any verify (never reap own child)
		}
		if !classifyReapCandidate(execByPID[p.pid], p.cmd, supportRoot, keepClean, verify) {
			continue
		}
		kill(p.pid)
		killed++
	}
	return killed, nil
}

// classifyReapCandidate reports whether a process (its resolved executable path
// execPath, possibly ""; and its kernel argv0 comm) is a reapable foreign focusd
// platform under supportRoot. keepClean (already filepath.Clean'd, or "") is the
// survivor's exec path exemption. See the file header for the tier semantics.
func classifyReapCandidate(execPath, comm, supportRoot, keepClean string, verify Verifier) bool {
	root := filepath.Clean(supportRoot) + string(filepath.Separator)

	// ---- SIGNATURE tier (primary, naming-agnostic) ----
	//
	// Anchor: under this mode's SupportRoot AND under a `.../bin/...` PLATFORM
	// layout. The bin-segment gate is the DAEMON/PLATFORM discriminator: the
	// platform binary always lives at <platform-workdir>/bin/<base>
	// (core.Store.BinPath), whereas the daemon mesh workers live DIRECTLY in
	// daemon-home (<support-root>/.<hidden>/<base>, relocate.RelocateInto — no bin
	// segment). Both are signed focusd binaries under SupportRoot, so signature +
	// root anchor ALONE cannot tell them apart — without this gate the reaper
	// would classify a signed daemon worker (self, peer, companion) as a reapable
	// "platform" and SIGKILL the mesh on a real signed install. The gate makes the
	// reaper structurally incapable of ever reaching a daemon binary.
	if execPath != "" {
		clean := filepath.Clean(execPath)
		if strings.HasPrefix(clean, root) && underBinSegment(clean, root) {
			ok, verr := verify(execPath)
			switch {
			case verr == nil && ok:
				// Genuine, signed focusd platform under our root.
				if keepClean != "" && clean == keepClean {
					return false // the survivor, exempt by path
				}
				return true
			case verr == nil && !ok:
				return false // present but UNSIGNED → mismatch, not ours → never reap
			case errors.Is(verr, fs.ErrNotExist):
				// Binary is gone → the deleted-binary case → fall through to the
				// ENOENT fallback below (keyed on the persisting kernel argv0).
			default:
				return false // any other read error → be safe, do not reap
			}
		}
	}

	// ---- DELETED-BINARY fallback (ENOENT only) ----
	// Reached when the signature tier saw fs.ErrNotExist (or no exec path was
	// resolvable). Key on the persisting kernel argv0 suffix, still anchored under
	// the root — the legacy `/bin/<semver>/platform` layout (the workdir-rm'd
	// orphan whose disguise was never applied, or a pre-HF4 install).
	if !filepath.IsAbs(comm) {
		return false // disguised/bare-token argv0 → no path → cannot anchor
	}
	if !platformSignatureRE.MatchString(comm) {
		return false
	}
	cleanComm := filepath.Clean(comm)
	if !strings.HasPrefix(cleanComm, root) {
		return false // signature present but NOT under this mode's root
	}
	if keepClean != "" && cleanComm == keepClean {
		return false // survivor exempt by path
	}
	return true
}

// underBinSegment reports whether cleanPath (already confirmed to be prefixed by
// rootWithSep) sits under a `.../bin/...` segment — the PLATFORM binary layout
// (<platform-workdir>/bin/<base>). The DAEMON binary lives directly in daemon-home
// (<support-root>/.<hidden>/<base>, no bin segment), so this is the structural
// discriminator that keeps a signed daemon mesh worker from ever being classified
// as a reapable platform. The match is SEGMENT-exact (a leading + trailing
// separator around "bin"), so a dir named "sbin"/"binary" never satisfies it; a
// trailing component after "bin/" is required (a file literally named "bin"
// directly under root is not a platform layout). The legacy ENOENT fallback keys
// on platformSignatureRE, which already embeds the same `/bin/` requirement.
func underBinSegment(cleanPath, rootWithSep string) bool {
	rel := strings.TrimPrefix(cleanPath, rootWithSep)
	sep := string(filepath.Separator)
	return strings.Contains(sep+rel, sep+"bin"+sep)
}

// listPlatformProcs enumerates every process as (pid, argv0) via `ps`. `-axww` =
// all processes, unlimited width (so a long argv0 path is not truncated); `-o
// pid=,comm=` = no header, pid then argv0. On macOS `comm` is argv0 (a bare token
// under HF4 disguise); the anchored executable path used by the primary tier is
// resolved separately by resolvePlatformExecs.
func listPlatformProcs() ([]rawProc, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	var procs []rawProc
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			continue // pid with no argv0 — skip
		}
		pid, perr := strconv.Atoi(line[:sp])
		if perr != nil {
			continue
		}
		procs = append(procs, rawProc{pid: pid, cmd: strings.TrimSpace(line[sp+1:])})
	}
	return procs, nil
}

// resolvePlatformExecs maps each of the ALREADY-ENUMERATED procs to its main
// executable path (the reaper hands us the same `ps` snapshot it listed, so we
// never shell out to `ps` a second time).
//
// PRIMARY: libproc proc_pidpath per pid (see proc_exec_darwin.go) —
// kernel-authoritative, no subprocess, and disguise-proof (it returns the real
// exec path regardless of the HF4 bare-token argv0). This is the "enumerate by
// exe" seam the reaper needs so the signature tier fires against a disguised
// orphan the argv0 fallback could never anchor.
//
// FALLBACK: the legacy system-wide `lsof -d txt` scan, used only if libproc
// resolves NOTHING for the whole set (e.g. a future SYS_proc_info ABI change, or
// an empty proc list) — so the reaper never goes blind. A PARTIAL libproc failure
// (some pids resolved, some not) deliberately does NOT trigger lsof: an
// unresolved pid degrades to the ENOENT/argv0 tier, which can only UNDER-reap (a
// disguised bare-token argv0 fails the anchored regex), never over-reap — a safe
// default that avoids the heavy (~180ms) lsof scan on the common path.
func resolvePlatformExecs(procs []rawProc) (map[int]string, error) {
	m := make(map[int]string, len(procs))
	for _, p := range procs {
		if path, perr := procPidPath(p.pid); perr == nil && path != "" {
			m[p.pid] = path
		}
	}
	if len(m) == 0 {
		return resolveExecsViaLsof(), nil
	}
	return m, nil
}

// resolveExecsViaLsof is the fallback pid → executable resolver: a SINGLE
// system-wide `lsof -d txt -Fpn`: `-d txt` restricts to text (executable) fds,
// `-Fpn` emits parseable p(id) and n(ame) fields. The FIRST `n` after each `p` is
// the process's own executable (dyld/dylibs follow). lsof commonly exits non-zero
// when a few fds are unreadable while still emitting valid stdout, so we PARSE the
// captured output regardless of exit status (best-effort). Even after the binary
// is unlinked the vnode persists, so lsof still reports the (now dangling) path —
// which sig.VerifyFile then classifies as fs.ErrNotExist.
func resolveExecsViaLsof() map[int]string {
	out, _ := exec.Command("lsof", "-d", "txt", "-Fpn").Output()
	m := make(map[int]string)
	cur := 0
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, perr := strconv.Atoi(line[1:])
			if perr != nil {
				cur = 0
				continue
			}
			cur = pid
		case 'n':
			if cur > 0 {
				if _, seen := m[cur]; !seen {
					m[cur] = line[1:] // first txt path = the executable
				}
			}
		}
	}
	return m
}

// killProc delivers SIGTERM then SIGKILL back-to-back with NO grace interval —
// this is an immediate kill, not a graceful drain. An orphaned platform we are
// reaping holds only disposable state (nothing to flush), and a per-pid grace
// window would stall the reconcile tick when several orphans have accreted. The
// SIGTERM is sent first only so a process that installs a fast terminate handler
// can exit cleanly; SIGKILL immediately after guarantees death regardless.
func killProc(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	_ = proc.Signal(syscall.SIGKILL)
}
