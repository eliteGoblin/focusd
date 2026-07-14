package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
)

// Discovered is the outcome of inspecting one plugin directory.
type Discovered struct {
	Manifest *Manifest
	Dir      string
	// BinaryPath is the resolved executable for the current host. Empty
	// when the plugin was rejected before/at binary resolution.
	BinaryPath string
	// OK is true only if the plugin is valid AND runnable in this mode.
	OK bool
	// Reason explains a rejection (empty when OK).
	Reason string
	// Expected marks a rejection that is a NORMAL consequence of this
	// install's environment (wrong host os/arch, or a system plugin under a
	// user-mode platform / vice-versa) rather than a defect (corrupt
	// manifest, unsupported protocol, security violation). The bundle ships
	// every plugin to every install, so a mode/host-mismatched plugin is
	// rejected on every clean startup — that is steady state, not a problem.
	// Logging it at WARN would pollute the whitebox log; the composition
	// root logs Expected rejections at INFO instead (FEATURE 16).
	Expected bool
	// Restored is true when the authenticity check at the START of evaluate
	// (FEATURE 23, Fix 1) had to rewrite one of this plugin's on-disk files
	// back to the genuine embedded copy — i.e. a tamper was found and repaired
	// during discovery, BEFORE the manifest was parsed. The composition root
	// records this as a tamper event (the runner's point-of-use check would
	// otherwise never see it, since discovery already restored the genuine
	// files). TamperWant/TamperGot are the sha prefixes of the genuine vs the
	// on-disk (tampered) first-mismatched file — never a path.
	Restored              bool
	TamperWant, TamperGot string
}

// integrityGuard is the discovery-time authenticity seam (FEATURE 23,
// ADR-0019). It confirms a plugin directory is genuine BEFORE its manifest is
// read: VerifyOrRestore reconciles the on-disk files against the signed
// embedded copy (so a swapped plugin.json can't redirect the entrypoint), and
// IsBundled gates the system-mode allowlist (a plugin dir absent from the
// signed bundle is refused). Production wires the bundle-backed impl; tests
// inject a fake or leave it nil (integrity gating skipped — legacy behaviour).
type integrityGuard interface {
	VerifyOrRestore(pluginRoot, subdir string) (restored bool, wantPrefix, gotPrefix string, err error)
	IsBundled(subdir string) bool
}

// Discoverer scans a plugin root and decides which plugins this platform
// instance may run, given its OS/arch and run mode.
type Discoverer struct {
	GOOS, GOARCH string
	Mode         osadapter.RunMode
	// guard is the discovery-time authenticity check (FEATURE 23). nil =>
	// skipped (manifest read/trusted as-is, legacy behaviour). The composition
	// root injects the bundle-backed impl via WithIntegrity.
	guard integrityGuard
}

// NewDiscoverer builds a Discoverer for the current host and given mode.
func NewDiscoverer(mode osadapter.RunMode) *Discoverer {
	return &Discoverer{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Mode: mode}
}

// WithIntegrity wires the discovery-time authenticity guard (FEATURE 23) and
// returns the same *Discoverer for fluent construction. A nil guard is
// ignored (integrity gating stays off).
func (d *Discoverer) WithIntegrity(g integrityGuard) *Discoverer {
	if g != nil {
		d.guard = g
	}
	return d
}

// Discover scans every immediate subdirectory of pluginRoot for a
// plugin.json and evaluates it. Missing root => empty result, no error
// (a fresh install legitimately has no plugins yet).
func (d *Discoverer) Discover(pluginRoot string) ([]Discovered, error) {
	entries, err := os.ReadDir(pluginRoot)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plugin root %s: %w", pluginRoot, err)
	}

	var out []Discovered
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(pluginRoot, e.Name())
		out = append(out, d.evaluate(dir))
	}
	return out, nil
}

func reject(dir, reason string, m *Manifest) Discovered {
	return Discovered{Manifest: m, Dir: dir, OK: false, Reason: reason}
}

// rejectExpected is reject for a NORMAL environment mismatch (wrong host,
// or a plugin whose run mode this install can't serve). It sets Expected so
// the composition root logs it at INFO, keeping the whitebox log quiet in
// steady state (FEATURE 16).
func rejectExpected(dir, reason string, m *Manifest) Discovered {
	d := reject(dir, reason, m)
	d.Expected = true
	return d
}

func (d *Discoverer) evaluate(dir string) (result Discovered) {
	// Thread the authenticity-restore signal onto WHICHEVER Discovered this
	// function returns — success OR any downstream rejection. A plugin can be
	// tampered AND then fail a later gate (host/protocol/perms/missing binary);
	// VerifyOrRestore still repaired it on disk, so the composition root must
	// still see Restored=true to record the tamper event. Setting it only on the
	// OK path would drop the audit trail in exactly that adversarial case
	// (go-reviewer HIGH). A named return + defer attaches it uniformly; the
	// closure vars stay zero for the two returns that precede the guard call, so
	// those are correctly left untouched.
	var restored bool
	var wantPrefix, gotPrefix string
	defer func() {
		if restored {
			result.Restored = true
			result.TamperWant = wantPrefix
			result.TamperGot = gotPrefix
		}
	}()

	// 0. Authenticity gate (FEATURE 23, ADR-0019) — runs BEFORE the manifest
	// is read, because everything downstream (entrypoint resolution, run_as)
	// trusts plugin.json, and plugin.json is on disk where a root attacker can
	// rewrite it. Two checks, in order:
	//
	//   (a) Fix 3 — system-mode allowlist: a plugin directory that is NOT part
	//       of the signed embedded bundle must never be scheduled by a
	//       system/root platform. VerifyOrRestore is a benign no-op for an
	//       unknown dir, so without this explicit membership check a rogue
	//       extra dir carrying a hand-written valid manifest could be run.
	//       (User-mode platforms may run out-of-bundle plugins — no allowlist.)
	//
	//   (b) Fix 1 — verify-before-parse: reconcile the plugin's on-disk files
	//       against the genuine embedded copy. A tampered plugin.json (e.g. a
	//       redirected entrypoint) is restored to genuine here, so ParseManifest
	//       below reads authentic bytes. A restore is surfaced (Restored +
	//       prefixes) so the composition root can record the tamper event — the
	//       runner's point-of-use check would otherwise never see it.
	if d.guard != nil {
		clean := filepath.Clean(dir)
		subdir := filepath.Base(clean)
		pluginRoot := filepath.Dir(clean)
		if d.Mode == osadapter.ModeSystem && !d.guard.IsBundled(subdir) {
			return reject(dir, "system-mode refuses plugin directory not in the signed bundle", nil)
		}
		var verr error
		restored, wantPrefix, gotPrefix, verr = d.guard.VerifyOrRestore(pluginRoot, subdir)
		if verr != nil {
			// The integrity check itself failed (disk unreadable, etc.). Do NOT
			// trust the manifest on disk — reject rather than proceed. Redact:
			// keep the error CLASS, never its raw string (may embed a path).
			return reject(dir, fmt.Sprintf("plugin integrity verify failed: %T", verr), nil)
		}
	}

	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return reject(dir, fmt.Sprintf("read plugin.json: %v", err), nil)
	}
	m, err := ParseManifest(raw)
	if err != nil {
		return reject(dir, err.Error(), nil)
	}

	// 1. Protocol gate — never execute unknown protocol versions.
	if !m.ProtocolSupported() {
		return reject(dir, fmt.Sprintf("unsupported protocol_version %q (supported: %v)",
			m.ProtocolVersion, SupportedProtocols), m)
	}

	// 2. Host gate — OS/arch must match. A host mismatch is an EXPECTED
	// rejection: the cross-platform bundle ships plugins for other OSes too.
	if !m.SupportsHost(d.GOOS, d.GOARCH) {
		return rejectExpected(dir, fmt.Sprintf("unsupported host %s/%s (plugin supports os=%v arch=%v)",
			d.GOOS, d.GOARCH, m.SupportedOS, m.SupportedArch), m)
	}

	// 3. Privilege gate — modes are fully isolated. A user-mode platform
	// must never run a system plugin; do not silently elevate. This is an
	// EXPECTED rejection: the bundle ships system plugins to user installs
	// too; they're simply not servable here (reinstall with admin for them).
	if d.Mode == osadapter.ModeUser && m.RequiredPrivilege == PrivSystem {
		return rejectExpected(dir, "system plugin cannot run under user-mode platform", m)
	}

	// 4. Security gate — a system/root platform must never execute a
	// plugin from a user-writable directory (spec §Important security
	// rule). User-writable here = group/other write bit set.
	if d.Mode == osadapter.ModeSystem {
		writable, werr := dirIsUserWritable(dir)
		if werr != nil {
			return reject(dir, fmt.Sprintf("inspect plugin dir perms: %v", werr), m)
		}
		if writable {
			return reject(dir, "system-mode refuses plugin from user-writable directory", m)
		}
	}

	// 5. Resolve the executable for this host.
	bin, berr := resolveBinary(dir, m, d.GOOS, d.GOARCH)
	if berr != nil {
		return reject(dir, berr.Error(), m)
	}

	// Restored/TamperWant/TamperGot are attached by the deferred closure above,
	// uniformly with every rejection path.
	return Discovered{Manifest: m, Dir: dir, BinaryPath: bin, OK: true}
}

// resolveBinary finds the plugin executable, trying the manifest's
// entrypoint first, then the bin/<os>-<arch>/<id> package layout.
func resolveBinary(dir string, m *Manifest, goos, goarch string) (string, error) {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	candidates := []string{
		filepath.Join(dir, filepath.Clean(m.Entrypoint)),
		filepath.Join(dir, "bin", goos+"-"+goarch, m.ID+ext),
	}
	for _, c := range candidates {
		// Containment check: never resolve outside the plugin dir.
		if rel, err := filepath.Rel(dir, c); err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		fi, err := os.Stat(c)
		if err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("no executable found (tried %v)", candidates)
}

// dirIsUserWritable reports whether path is writable by group or other.
func dirIsUserWritable(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.Mode().Perm()&0o022 != 0, nil
}
