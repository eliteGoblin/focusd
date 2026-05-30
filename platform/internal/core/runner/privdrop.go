// Privilege-drop: when a system-mode (root) platform must run a plugin
// whose manifest says run_as=current_user, the runner forks and setuids
// to the logged-in console user BEFORE exec, so the plugin writes the
// user's files as the user (never as root → /var/root/.claude corruption).
//
// Three silent-corruption edges are handled here and unit-tested:
//
//   - HOME: root's HOME is /var/root. The dropped child MUST get
//     HOME=/Users/<console-user> or skill files land in root's home.
//   - TMPDIR: root's TMPDIR (/var/folders/.../-Tmp-/ owned 0700 by root)
//     is inaccessible to the dropped uid; CreateTemp would fail. We pin
//     TMPDIR=/tmp (world-writable, sticky) for the child.
//   - no console user: at loginwindow / fast-user-switch, `stat -f %u
//     /dev/console` returns 0 (root). Running the plugin then would write
//     as root. We SKIP the tick instead (retry next schedule).
package runner

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
	"github.com/eliteGoblin/focusd/platform/internal/osadapter"
)

// dropAction is what runOnce should do about privileges for one exec.
type dropAction int

const (
	// dropNone: run the plugin with the platform's own credentials
	// (system→root for system plugins; user→the logged-in user already).
	dropNone dropAction = iota
	// dropToUser: system platform must setuid to the console user before
	// exec (run_as=current_user under root). plan carries the credentials.
	dropToUser
	// dropSkipNoConsoleUser: system platform wants to drop to the console
	// user but no one is logged in at the screen (console uid 0). Skip the
	// tick rather than write the user's files as root.
	dropSkipNoConsoleUser
)

// dropPlan is the resolved privilege-drop decision for one exec.
type dropPlan struct {
	action dropAction
	// Populated only when action == dropToUser.
	uid     int
	gid     int
	homeEnv string // HOME=/Users/<name>
	userEnv string // USER=<name>
	logEnv  string // LOGNAME=<name>
}

// consoleUserFn discovers the logged-in console user's identity. The
// real implementation shells out to `stat -f %u /dev/console` (no cgo)
// and resolves the username/home/gid via os/user. Tests inject a fake
// to exercise the no-console-user skip and the env-reseed paths without
// being root or touching /dev/console.
//
// Returns (uid, gid, name, home, err). A uid of 0 means "no console
// user" (loginwindow / fast-user-switch) and MUST trigger the skip.
type consoleUserFn func() (uid, gid int, name, home string, err error)

// resolvePlan decides what to do for a (mode, run_as) pair. It is the
// pure can-this-exec-proceed core, unit-tested via a fake consoleUser.
//
//   - user platform: current_user runs natively (we ARE the user);
//     system plugins never reach here (the scheduler gates them).
//   - system platform: system plugins run natively as root; current_user
//     plugins drop to the console user, or skip if none is logged in.
func resolvePlan(mode osadapter.RunMode, runAs string, consoleUser consoleUserFn) (dropPlan, error) {
	// Only a root/system platform ever needs to step down. In every other
	// case the platform already holds the right identity for the plugin.
	if mode != osadapter.ModeSystem {
		return dropPlan{action: dropNone}, nil
	}
	switch runAs {
	case plugin.RunAsCurrentUser, plugin.RunAsActiveUser:
		uid, gid, name, home, err := consoleUser()
		if err != nil {
			return dropPlan{}, fmt.Errorf("discover console user: %w", err)
		}
		if uid == 0 {
			// No one logged in at the screen (loginwindow / FUS). Running
			// now would write the user's files as root → corruption. Skip.
			return dropPlan{action: dropSkipNoConsoleUser}, nil
		}
		return dropPlan{
			action:  dropToUser,
			uid:     uid,
			gid:     gid,
			homeEnv: "HOME=" + home,
			userEnv: "USER=" + name,
			logEnv:  "LOGNAME=" + name,
		}, nil
	default:
		// system (or legacy "") plugin under a root platform: run as root.
		return dropPlan{action: dropNone}, nil
	}
}

// dropEnv is the reseeded environment for a privilege-dropped child.
// Root's environment (HOME=/var/root, TMPDIR=/var/folders/.../root-only)
// would corrupt or break the child, so we hand it an explicit, sane set
// rather than inheriting the platform's. PATH is a conventional default;
// the plugin contract does not depend on the platform's PATH.
func (p dropPlan) dropEnv() []string {
	return []string{
		p.homeEnv,
		p.userEnv,
		p.logEnv,
		"PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		// Root's TMPDIR is unreadable to the dropped uid → CreateTemp
		// fails. /tmp is world-writable + sticky, safe for any uid.
		"TMPDIR=/tmp",
	}
}

// prepareDropPaths makes a privilege-dropped child able to exec the
// plugin binary. The disguised workdir is root-owned 0700 (hidden from
// casual `ls`), so a child dropped to the console user cannot, by
// default, traverse down to the binary or read it — exec fails with
// "permission denied" (the real-hardware gap the 0777-tmpdir integration
// test originally masked).
//
// We widen the MINIMUM: read+execute on the binary itself, and traverse
// (execute, NOT read) on each ancestor directory that lacks it, up to a
// directory that is already world-traversable (system dirs like
// /Library/Application Support are 0755 and are left untouched). Adding
// only the execute bit to directories means their contents stay
// un-listable — disguise-by-enumeration is preserved; a dropped user can
// reach a binary whose exact path it already knows but cannot discover
// the tree. The binary stays root-OWNED (we only widen its mode), so the
// user can run it but cannot modify or neuter it.
func prepareDropPaths(binPath string) error {
	// Binary: the kernel must read the Mach-O to map it, and the exec bit
	// must be set for a non-owner. Add r-x for "other".
	if err := addPerm(binPath, 0o005); err != nil {
		return fmt.Errorf("make plugin binary user-executable: %w", err)
	}
	// Ancestors: add traverse (execute only) wherever missing. Walk all
	// the way up — a mid-chain dir may already be 0755 while a higher one
	// is still 0700, so we must not stop at the first traversable dir. We
	// only chmod dirs that lack o+x, so already-traversable system dirs are
	// left exactly as they are. Never touch "/".
	dir := filepath.Dir(binPath)
	for dir != "/" && dir != "." {
		if err := addPermIfMissing(dir, 0o001); err != nil {
			return fmt.Errorf("make %s traversable: %w", dir, err)
		}
		dir = filepath.Dir(dir)
	}
	return nil
}

// addPerm ORs the given permission bits into path's mode.
func addPerm(path string, bits os.FileMode) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.Chmod(path, fi.Mode().Perm()|bits)
}

// addPermIfMissing ORs bits into path's mode only when they're not
// already set — avoids needless chmod (and mtime churn) on system dirs
// that are already traversable.
func addPermIfMissing(path string, bits os.FileMode) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&bits == bits {
		return nil
	}
	return os.Chmod(path, fi.Mode().Perm()|bits)
}

// realConsoleUser is the production console-user discovery. It shells
// out to `stat -f %u /dev/console` (darwin) to get the uid, then resolves
// the rest via os/user. See consoleUserDiscover (platform-specific).
func realConsoleUser() (uid, gid int, name, home string, err error) {
	uid, err = consoleUID()
	if err != nil {
		return 0, 0, "", "", err
	}
	if uid == 0 {
		return 0, 0, "", "", nil // caller maps uid 0 → skip
	}
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("lookup uid %d: %w", uid, err)
	}
	gid, err = strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("parse gid %q: %w", u.Gid, err)
	}
	return uid, gid, u.Username, u.HomeDir, nil
}
