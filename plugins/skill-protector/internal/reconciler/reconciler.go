// Package reconciler is the pure logic for the skill-protector plugin:
// hash three embedded artifacts (skill, rule, hook script) against
// what is on disk, atomically rewrite any drift, then merge the focusd
// SessionStart hook into ~/.claude/settings.json.
//
// It writes only to paths under <HomeDir>/.claude/ and never invokes
// any external process. The HomeDir field is the only OS-bound input;
// tests inject t.TempDir() so nothing real is touched.
package reconciler

import (
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eliteGoblin/focusd/plugins/skill-protector/internal/settingsjson"
)

//go:embed data/SKILL.md data/rule.md data/hook.sh
var embedded embed.FS

// Reconciler holds the test seam (HomeDir / Now). All other state is
// embedded at compile time.
type Reconciler struct {
	HomeDir string
	Now     func() time.Time
}

// New builds a Reconciler rooted at homeDir.
func New(homeDir string) *Reconciler {
	return &Reconciler{HomeDir: homeDir, Now: time.Now}
}

// Outcome summarizes what one Reconcile pass did. It is the shape the
// command emits as JSON to stdout.
type Outcome struct {
	Written        []string `json:"written"`
	Noop           int      `json:"noop"`
	SettingsStatus string   `json:"settings_status"`
	SettingsError  string   `json:"settings_error,omitempty"`
}

// target describes one canonical artifact under HomeDir.
type target struct {
	path string      // absolute on-disk path
	want []byte      // embedded canonical content
	mode os.FileMode // file perm to apply
}

// Reconcile inspects each artifact, rewrites any drift, then merges
// settings.json. The error return is non-nil only when settings.json
// merge fails; content drift is repaired silently (recorded in the
// Outcome).
func (r *Reconciler) Reconcile() (Outcome, error) {
	if r.HomeDir == "" {
		return Outcome{}, errors.New("reconciler: HomeDir is empty")
	}
	// Refuse to write under any symlinked path inside ~/.claude — the
	// user-as-attacker model is explicit (they can pre-plant a symlink
	// at any depth: ~/.claude → /etc, OR ~/.claude/skills → /etc, OR
	// ~/.claude/skills/focusd-protection → /etc, etc.). Reject before
	// any MkdirAll / write would follow the symlink off-tree.
	// (Security-reviewer MEDIUM, Copilot follow-up — deeper paths.)
	// Non-existent ancestors are fine: AtomicWrite's MkdirAll creates
	// them as real dirs with 0o700. TOCTOU window between check and
	// write is closed by the 5-min reconcile loop.
	claudeDir := filepath.Join(r.HomeDir, ".claude")
	for _, leaf := range []string{
		filepath.Join(r.HomeDir, ".claude", "skills", "focusd-protection"),
		filepath.Join(r.HomeDir, ".claude", "rules", "frank"),
		filepath.Join(r.HomeDir, ".claude", "hooks"),
	} {
		if err := assertNoSymlinkAncestors(claudeDir, leaf); err != nil {
			return Outcome{}, err
		}
	}
	skill, err := embedded.ReadFile("data/SKILL.md")
	if err != nil {
		return Outcome{}, fmt.Errorf("read embedded SKILL.md: %w", err)
	}
	rule, err := embedded.ReadFile("data/rule.md")
	if err != nil {
		return Outcome{}, fmt.Errorf("read embedded rule.md: %w", err)
	}
	hook, err := embedded.ReadFile("data/hook.sh")
	if err != nil {
		return Outcome{}, fmt.Errorf("read embedded hook.sh: %w", err)
	}

	skillPath := filepath.Join(r.HomeDir, ".claude", "skills", "focusd-protection", "SKILL.md")
	rulePath := filepath.Join(r.HomeDir, ".claude", "rules", "frank", "focusd-protection.md")
	hookPath := filepath.Join(r.HomeDir, ".claude", "hooks", "focusd-protection-reinject.sh")
	settingsPath := filepath.Join(r.HomeDir, ".claude", "settings.json")

	targets := []target{
		{path: skillPath, want: skill, mode: 0o600},
		{path: rulePath, want: rule, mode: 0o600},
		{path: hookPath, want: hook, mode: 0o700},
	}

	out := Outcome{}
	for _, t := range targets {
		wrote, err := reconcileFile(t)
		if err != nil {
			return out, fmt.Errorf("reconcile %s: %w", t.path, err)
		}
		if wrote {
			out.Written = append(out.Written, t.path)
		} else {
			out.Noop++
		}
	}
	sort.Strings(out.Written)

	// Merge settings.json last so the content files are guaranteed
	// present even if settings is malformed and we have to abort it.
	if err := settingsjson.Merge(settingsPath, hookPath); err != nil {
		out.SettingsStatus = "error"
		out.SettingsError = err.Error()
		return out, err
	}
	out.SettingsStatus = "ok"
	return out, nil
}

// assertNoSymlinkAncestors walks from rootDir down to leafDir (both
// must be absolute, rootDir must be a prefix of leafDir) and rejects
// if any EXISTING segment is a symlink. Non-existent segments are
// ignored — the caller will create them as real dirs with MkdirAll.
//
// Why: the user-as-attacker model allows pre-planting a symlink at
// any depth under ~/.claude. A simple `os.Lstat("~/.claude")` only
// catches the root; this catches every intermediate dir we'll later
// MkdirAll into or AtomicWrite under.
func assertNoSymlinkAncestors(rootDir, leafDir string) error {
	rel, err := filepath.Rel(rootDir, leafDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("internal: %q is not under %q", leafDir, rootDir)
	}
	cur := rootDir
	segments := append([]string{""}, strings.Split(rel, string(filepath.Separator))...)
	for _, seg := range segments {
		if seg != "" {
			cur = filepath.Join(cur, seg)
		}
		info, err := os.Lstat(cur)
		if err != nil {
			return nil // non-existent ancestor; MkdirAll will create real dir
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(cur)
			return fmt.Errorf("reconciler: refusing to write — %s is a symlink "+
				"(target: %s); user-as-attacker model rejects pre-planted symlinks",
				cur, target)
		}
	}
	return nil
}

// reconcileFile returns (wrote, err). wrote=false means on-disk
// content already matched the canonical bytes — no work performed.
func reconcileFile(t target) (bool, error) {
	current, err := os.ReadFile(t.path)
	switch {
	case err == nil:
		if sha256.Sum256(current) == sha256.Sum256(t.want) {
			return false, nil
		}
	case errors.Is(err, fs.ErrNotExist):
		// fall through to write
	default:
		return false, fmt.Errorf("read: %w", err)
	}
	// AtomicWrite creates the parent dir (0o700) internally; no
	// separate MkdirAll needed. Single source of truth for the write
	// primitive lives in settingsjson. (Go-reviewer HIGH.)
	if err := settingsjson.AtomicWrite(t.path, t.want, t.mode); err != nil {
		return false, err
	}
	return true, nil
}
