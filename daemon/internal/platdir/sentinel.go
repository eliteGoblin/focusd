package platdir

// FEATURE 26 — content-magic directory sentinels (the destructive-safety core).
//
// Dropping the leading dot from disguised directory names (relocate.FEATURE 26)
// means the generation sweeps can no longer pre-filter candidates by a "."
// prefix — they must now stat INSIDE real app-support folders (Google/, Spotify/)
// to decide what is ours. A mis-identification here would `os.RemoveAll` a REAL
// app's data, so recognition is deliberately CONTENT-based and positive-only:
//
//   - Each directory WE create carries a small sentinel file whose CONTENT is a
//     fixed MAGIC constant XOR-masked with a binary-global key. Two DISTINCT
//     magics separate a platform-workdir from a daemon-home.
//   - Recognition ([hasMarker]) SCANS a candidate directory for a regular file
//     whose size equals the sentinel size AND whose bytes un-mask to the magic.
//     A real app folder has neither magic → is NEVER a delete candidate. Nothing
//     but a positive magic match can gate a RemoveAll (the sweep rule).
//
// Why content, not the salt-derived basename: a sweep inspects OTHER generations'
// directories, whose salt it does not hold, so it cannot know their sentinel
// basenames. The magic + mask are binary-global (built at runtime from split
// literals, like core.deriveRosterMask, so a `strings` dump finds no contiguous
// constant), so ANY generation recognises ANY other — while the basename stays
// crypto/rand (de-patterned) because recognition never relies on it.
//
// Casual-grade friction only: a source/binary reader recovers the magic + mask
// and could forge a sentinel. The goal is solely that a casual grep/ls/find — and
// an accidental self-delete — cannot touch the wrong folder.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"os"
	"path/filepath"
)

// magicLen is the byte length of a directory magic (and therefore the exact size
// of a sentinel file). 16 bytes → a false-positive against arbitrary real-app
// file content is ~2^-128; combined with the exact-size gate it is unreachable.
const magicLen = 16

// maxSentinelScan bounds how many equal-sized regular files a single directory
// scan will read, so a pathological folder cannot make a sweep read unboundedly.
// Our own directories hold a handful of files, so the sentinel is always found
// well within this budget; real app folders rarely hold many exactly-16-byte
// files, and each read is only 16 bytes.
const maxSentinelScan = 1024

// magicMaskKey is the binary-global 32-byte XOR key for sentinel content. Built
// at runtime from two literals (never one contiguous constant) so a `strings`
// dump of the binary finds neither the key nor a match — the same discipline as
// core.deriveRosterMask.
func magicMaskKey() [32]byte {
	a := "focusd-pwd-dh-"
	b := "sentinel-mask-v1"
	return sha256.Sum256([]byte(a + b))
}

// pwdMagic is the platform-workdir magic (the un-masked sentinel content).
func pwdMagic() []byte {
	h := sha256.Sum256([]byte("focusd-platform-" + "workdir-magic-v1"))
	return h[:magicLen]
}

// dhMagic is the daemon-home magic. DISTINCT from pwdMagic so a platform-workdir
// is never mistaken for a daemon-home (or vice-versa) — the sweeps rely on that
// separation to preserve "never delete a daemon-home".
func dhMagic() []byte {
	h := sha256.Sum256([]byte("focusd-daemon-" + "home-magic-v1"))
	return h[:magicLen]
}

// maskedSentinel returns the on-disk sentinel bytes for a magic: magic XOR key.
func maskedSentinel(magic []byte) []byte {
	key := magicMaskKey()
	out := make([]byte, len(magic))
	for i := range magic {
		out[i] = magic[i] ^ key[i%len(key)]
	}
	return out
}

// writeSentinel writes a magic sentinel into dir under a crypto/rand plausible,
// dot-hidden basename (relocate.sentinelPool via [randomSentinelBasename]). The
// basename is random (de-patterned — no fixed `find -name` literal); recognition
// is by CONTENT so the basename never needs to be recomputed. Best-effort: a
// write failure only weakens later recognition, it must not fail the create.
func writeSentinel(dir string, magic []byte) {
	_ = os.WriteFile(filepath.Join(dir, randomSentinelBasename()), maskedSentinel(magic), 0o600)
}

// hasMarker reports whether dir contains a sentinel file matching magic. It reads
// ONLY regular files whose size already equals magicLen (a cheap dirent/stat
// gate — real files are almost never exactly that size), then compares the
// un-masked... rather, the on-disk bytes against the pre-masked magic in constant
// time. Returns true on the FIRST match. A directory it cannot read → false (not
// ours → never deleted).
//
// This is the ONLY predicate permitted to gate a RemoveAll in the sweeps.
func hasMarker(dir string, magic []byte) bool {
	want := maskedSentinel(magic)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	scanned := 0
	for _, e := range entries {
		if scanned >= maxSentinelScan {
			break
		}
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || !info.Mode().IsRegular() || info.Size() != int64(magicLen) {
			continue // symlink / wrong size → cannot be our sentinel (no read)
		}
		scanned++
		b, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil || len(b) != len(want) {
			continue
		}
		if subtle.ConstantTimeCompare(b, want) == 1 {
			return true
		}
	}
	return false
}

// --- legacy (pre-FEATURE-26) migration recognition -----------------------------

// legacyPwdSentinelBasename is the FIXED sentinel basename a pre-FEATURE-26
// platform-workdir carried (an empty 0-byte file). New workdirs use a random
// basename + magic content instead; this literal is retained ONLY so an upgrade
// can still recognise — and sweep — an old-scheme platform-workdir left on disk.
const legacyPwdSentinelBasename = ".com.apple.metadata.pwd.plist"

// legacyStateDBBasename is the platform engine's SQLite state file. A pre-F26
// platform-workdir always holds it. Required ALONGSIDE the legacy sentinel below
// so recognition is a TWO-signal positive match, never a bare name heuristic.
const legacyStateDBBasename = "state.db"

// isLegacyPlatformWorkdir reports whether dir is a pre-FEATURE-26 platform-workdir
// still on disk (migration cleanup). It demands BOTH focusd-authored signals — the
// exact legacy sentinel basename AND the engine's state.db — so no real app folder
// (which has neither, let alone both) can ever match. This is the sole
// name-referencing recognition, scoped strictly to legacy migration and gated by a
// second content-file requirement; the forward scheme is pure content magic.
func isLegacyPlatformWorkdir(dir string) bool {
	if fi, err := os.Stat(filepath.Join(dir, legacyPwdSentinelBasename)); err != nil || fi.IsDir() {
		return false
	}
	if fi, err := os.Stat(filepath.Join(dir, legacyStateDBBasename)); err != nil || fi.IsDir() {
		return false
	}
	return true
}

// randomSentinelBasename picks a plausible dot-hidden sentinel basename at random
// (crypto/rand). Duplicated small pool here (not imported) keeps platdir free of
// a relocate dependency cycle for this leaf helper; the pool mirrors
// relocate.sentinelPool in intent (neutral OS-metadata-looking names).
func randomSentinelBasename() string {
	var b [1]byte
	_, _ = rand.Read(b[:])
	return sentinelBasenames[int(b[0])%len(sentinelBasenames)]
}

// sentinelBasenames are neutral, dot-hidden, OS-metadata-looking basenames. A
// casual `ls -a` reads them as the usual macOS cruft; recognition is by content,
// so the specific name never matters.
var sentinelBasenames = []string{
	".DocumentRevisions.plist", ".apdisk", ".TemporaryItems.db",
	".fseventsd.cache", ".metadata_index", ".Trashes.db", ".VolumeIcon.dat",
	".com.apple.timemachine.donotpresent", ".mdimport.state",
	".quicklook.thumb", ".spotlight.shadow", ".caches.index",
}
