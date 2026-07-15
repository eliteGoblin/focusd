//go:build darwin

package status

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/mode"
	"github.com/eliteGoblin/focusd/daemon/internal/osadapter"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
	"github.com/eliteGoblin/focusd/daemon/internal/status/redact"
)

// warmupWindow is how young an install (by version.json mtime) may be, with
// no good version yet, and still read HEALTHY — warming up rather than DOWN.
const warmupWindow = 10 * time.Minute

// platformStatusTimeout caps the delegated `platform status` exec. On expiry
// the child is killed and platform detail is marked unavailable — the daemon
// still reports its own facts.
const platformStatusTimeout = 8 * time.Second

// Gather builds the daemon Snapshot and captures the delegated platform
// detail. This is the ONLY place that touches exec / the filesystem / raw
// disguised paths. Every disguised value (workdir, binary path) is confined
// to a redact.Token and only materialised inside a redact.Use closure to
// build an exec arg or a pgrep pattern — it never enters the Snapshot, an
// error string, or any rendered output.
//
// workdirOverride, when non-empty, is an operator-supplied workdir (a CLI
// flag, not a discovered disguised value); it takes the place of discovery.
//
// jsonMode selects which single `platform status` form to exec: the JSON
// report (true) or the text render (false). Exactly ONE is run per daemon
// invocation — the one the daemon will actually render — so the delegated
// child is never executed twice (no doubled timeout budget, no two snapshots).
func Gather(workdirOverride string, jsonMode bool) (Snapshot, PlatformDetail) {
	m := mode.Resolve()
	s := Snapshot{Mode: string(m)}

	// --- Mesh roles (counts only cross the osadapter boundary) ---
	loaded, total, found, err := osadapter.MeshStatus(m)
	if err != nil {
		// A probe failure (permission-denied read of a root-owned system
		// install queried without sudo, or any other IO error at this seam)
		// is bucketed to "unknown" — NEVER reported as DOWN. We can't tell
		// EACCES from a genuine no-install at this boundary, so we always
		// prefer the honest "unknown" over falsely calling a probe failure
		// a down engine.
		s.MeshUnknown = true
	} else {
		s.MeshLoaded = loaded
		s.MeshTotal = total
		s.Found = found
	}

	// --- Out-of-band recovery rail liveness (bools only) ---
	// FEATURE 18 / ADR-0020: the launchd COMPANION superseded the FEATURE 12
	// cron watchdog as the recovery rail. Report the COMPANION's liveness —
	// present (its binary on disk) + backupOK (its signed daemon backup passes
	// Ed25519 verification) — falling back to the legacy cron rail only when no
	// companion is present (a pre-F18 install mid-migration), so the status
	// never goes dark. Both probes cross ONLY bools (no path leaves the
	// osadapter boundary), so this cannot leak a disguised identifier.
	cPresent, cBackupOK := osadapter.CompanionStatus(m)
	cronPresent, cronCopyOK := osadapter.WatchdogStatus(m)
	railPresent, copyOK := recoveryRailStatus(cPresent, cBackupOK, cronPresent, cronCopyOK)
	s.WatchdogChecked = true
	s.WatchdogCron = railPresent
	s.WatchdogCopyOK = copyOK

	// --- Discover the install (workdir + binary path stay tokenised) ---
	var workdirTok redact.Token
	if workdirOverride != "" {
		workdirTok = redact.New(workdirOverride)
		s.Found = true
	} else {
		cur, ferr := osadapter.FindCurrentInstall(m, sig.VerifyFile)
		if ferr != nil {
			// Discovery IO failure on a system install without root → unknown.
			s.VersionsUnknown = true
		} else if cur.Workdir != "" {
			workdirTok = redact.New(cur.Workdir)
			s.Found = s.Found || cur.BinaryPath != ""
		}
	}

	// --- Versions from the store (read inside Use so the path stays hidden) ---
	if workdirTok.Present() && !s.VersionsUnknown {
		desired, good, vUnknown := readVersions(workdirTok)
		s.Desired = desired
		s.Good = good
		s.VersionsUnknown = vUnknown

		// Warming up: no good version yet AND install is younger than the
		// warmup window (derive age from version.json mtime, inside Use).
		if good == "" && !vUnknown {
			if age, ok := installAge(workdirTok); ok && age < warmupWindow {
				s.WarmingUp = true
			}
		}

		// Process count: match the EXACT good-binary path. Built + run inside
		// Use so the path (the secret) never escapes. Only meaningful when a
		// good version exists.
		if good != "" {
			s.ProcCount = procCount(workdirTok, good)
		}
	} else if !workdirTok.Present() && !s.VersionsUnknown && s.Found {
		// Found a mesh but couldn't recover a workdir → versions unknown.
		s.VersionsUnknown = true
	}

	// --- Delegate plugin detail to `platform status` ---
	pd := gatherPlatform(workdirTok, jsonMode)
	s.PlatformUnavailable = !pd.Available

	return s, pd
}

// readVersions reads desired + good from the store under the tokenised
// workdir. vUnknown=true when the workdir is unreadable (permission/absent) —
// distinct from "readable but no good promoted yet" (good=="").
func readVersions(workdir redact.Token) (desired, good string, vUnknown bool) {
	type versions struct {
		desired, good string
		vUnknown      bool
	}
	v := redact.Use(workdir, func(raw string) versions {
		// Distinguish EACCES (unknown) from a readable workdir with no good
		// file (genuine state). Stat the dir: if we can't even read it, unknown.
		if fi, err := os.Stat(raw); err != nil || !fi.IsDir() {
			if os.IsPermission(err) {
				return versions{vUnknown: true}
			}
			// Absent workdir: nothing to report, not an error.
			return versions{}
		}
		st := &core.Store{Dir: raw}
		return versions{desired: st.Desired(), good: st.Good()}
	})
	return v.desired, v.good, v.vUnknown
}

// installAge returns how long ago version.json was last written, used to tell
// "warming up" from "down". The path stays inside the Use closure.
func installAge(workdir redact.Token) (time.Duration, bool) {
	return redactUse2(workdir, func(raw string) (time.Duration, bool) {
		fi, err := os.Stat(filepath.Join(raw, core.VersionFile))
		if err != nil {
			return 0, false
		}
		return time.Since(fi.ModTime()), true
	})
}

// procCount counts live processes whose argv exactly matches the good
// platform binary path. The pattern is built AND run inside the Use closure
// so the path never escapes; pgrep's stderr/error is swallowed so it can
// never echo the pattern into status output. Exact match (-x) so we never
// match on a bare "platform" substring.
func procCount(workdir redact.Token, good string) int {
	return redact.Use(workdir, func(raw string) int {
		ctx, cancel := context.WithTimeout(context.Background(), platformStatusTimeout)
		defer cancel()
		binPath := (&core.Store{Dir: raw}).BinPath(good)
		// pgrep -f -x: match the full argument vector exactly. The platform
		// is started as `<binPath> --workdir <wd>`, so match that exact argv.
		pattern := binPath + " --workdir " + raw
		out, err := exec.CommandContext(ctx, "pgrep", "-f", "-x", pattern).Output()
		if err != nil {
			// Exit status 1 = no match (count 0); any other error we also
			// treat as 0 rather than leaking the pattern via an error string.
			return 0
		}
		n := 0
		for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
			if len(bytes.TrimSpace(line)) > 0 {
				n++
			}
		}
		return n
	})
}

// gatherPlatform execs `platform status` EXACTLY ONCE, in the single form the
// daemon will render: `--json` when jsonMode, otherwise text. Only the needed
// output is produced — no doubled exec, no two divergent snapshots. On timeout
// / non-zero exit / exec error it returns an unavailable detail — the daemon
// never fails on this. Platform STDERR is SWALLOWED (never forwarded, never
// interpolated into any error): it can contain disguised paths the platform
// logs about itself.
func gatherPlatform(workdir redact.Token, jsonMode bool) PlatformDetail {
	if !workdir.Present() {
		return PlatformDetail{Available: false}
	}
	// Run the good-version platform binary in `status` mode with the
	// discovered workdir. The binary path and the workdir both stay inside
	// the Use closure — they never escape into the returned detail.
	pd := redact.Use(workdir, func(raw string) PlatformDetail {
		st := &core.Store{Dir: raw}
		good := st.Good()
		if good == "" {
			// No good version → no platform process to query.
			return PlatformDetail{Available: false}
		}
		binPath := st.BinPath(good)
		out, exitCode, ran := runPlatformStatus(binPath, raw, jsonMode)

		// The command itself failed to produce a health verdict (exec error,
		// timeout, or exit >= 2) → unavailable. The daemon still reports its
		// own facts; this is a note, never a forced failure.
		if !ran {
			return PlatformDetail{Available: false}
		}

		if jsonMode {
			// A health verdict but EMPTY/INVALID json is not embeddable →
			// unavailable (we cannot trust an empty/garbage report).
			if out == "" || !json.Valid([]byte(out)) {
				return PlatformDetail{Available: false}
			}
			return PlatformDetail{
				Available: true,
				ExitCode:  exitCode,
				JSON:      json.RawMessage(out),
			}
		}
		// Text mode: empty output despite a verdict → unavailable.
		if out == "" {
			return PlatformDetail{Available: false}
		}
		return PlatformDetail{
			Available:  true,
			ExitCode:   exitCode,
			TextOutput: out,
		}
	})
	return pd
}

// runPlatformStatus execs `<binPath> status --workdir <wd> [--json]` with an
// 8s timeout and captures stdout into a buffer. STDERR is discarded (it may
// carry disguised paths). On timeout the child is killed via context
// cancellation.
//
// `platform status` is a HEALTH probe: it exits 0 (healthy/unknown) OR 1
// (degraded) and STILL produces valid, useful output in both cases. So a
// degraded platform (exit 1) ran SUCCESSFULLY — we must not treat it as
// unavailable and hide its real degradation. ran=true means the command
// genuinely produced a verdict (exit 0 or 1); exitCode carries which one so
// the caller can fold the platform verdict into the daemon's overall.
//
// ran=false (=> unavailable) only on: exec error, context timeout, or any exit
// code >= 2 (an internal-error/usage failure of the platform itself, not a
// health verdict). Empty/invalid output is judged by the caller per mode.
func runPlatformStatus(binPath, workdir string, asJSON bool) (out string, exitCode int, ran bool) {
	ctx, cancel := context.WithTimeout(context.Background(), platformStatusTimeout)
	defer cancel()

	args := []string{"status", "--workdir", workdir, "--no-color"}
	if asJSON {
		args = append(args, "--json")
	}
	cmd := exec.CommandContext(ctx, binPath, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil // swallow: platform stderr can contain disguised paths

	err := cmd.Run()
	// Context timeout: the child was killed → genuinely unavailable.
	if ctx.Err() != nil {
		return "", 0, false
	}
	if err == nil {
		return stdout.String(), 0, true // clean exit 0
	}
	// Non-zero exit: only exit 1 (DEGRADED) is still a valid health verdict.
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		if code == 1 {
			return stdout.String(), 1, true
		}
		// code >= 2 (or -1 on signal) => platform internal/usage failure.
		return "", code, false
	}
	// Exec error (binary missing, not startable) => unavailable.
	return "", 0, false
}

// redactUse2 is a two-return variant of redact.Use. redact.Use is generic
// over a single return; the gather needs pairs (text+json, age+ok), so we
// thread them through a struct-free closure here while keeping the raw value
// confined to the closure exactly as redact.Use does.
func redactUse2[A any, B any](t redact.Token, fn func(raw string) (A, B)) (A, B) {
	type pair struct {
		a A
		b B
	}
	p := redact.Use(t, func(raw string) pair {
		a, b := fn(raw)
		return pair{a, b}
	})
	return p.a, p.b
}
