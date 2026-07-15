package relocate

// FEATURE 26 — disguise BLEND + workdir hiding.
//
// The prior scheme named every hidden focusd directory ".<launchd-prefix>.<hex>"
// (HiddenWorkdir) — a leading dot + a shared 12-hex tail. That gave the whole
// install ONE visual signature: a `find ~/Library -name '.*.??????????'` (dotted
// + hex tail) or a `ls -a | grep '^\.'` scan clustered the daemon-home and every
// platform-workdir generation together, and the leading dot itself said "someone
// is hiding something here".
//
// This file drops the dot and the shared tail: each disguised directory is
// generated INDEPENDENTLY from a randomly-chosen SHAPE that mimics a DIFFERENT
// kind of ordinary ~/Library/Application Support entry (a plain app word, a
// CamelCase agent, a reverse-DNS bundle id, a bare vendor). No single glob spans
// the set, and each entry is individually indistinguishable from the dozens of
// real app-support folders already present.
//
// Names use crypto/rand per creation (NOT salt-derived): the daemon-home name
// predates the salt (the salt file lives INSIDE it) and must be stable for the
// install; each platform-workdir generation must LOOK like a different app. The
// salt stays the key only for RECOMPUTABLE things (bin basename, argv0, marker
// basenames, content masks — see disguise_platform.go / disguise_marker.go).
//
// Casual-grade friction only (register §5): a determined/AI reader of a plist or
// the binary still recovers the paths. The durable weight is off-device.

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// SaltBasename is the ONE fixed, neutral literal basename kept across installs:
// the per-install disguise salt file inside the daemon-home. Every OTHER marker
// basename is salt-DERIVED (disguise_marker.go), but the salt file itself cannot
// be (a reader needs a fixed name to bootstrap the salt). Neutral + dot-hidden so
// a casual `ls -a` in the (already hidden) daemon-home does not flag it, and
// generic enough that a full-disk `find -name` hit reveals nothing product-named.
// Single source of truth: core.InstallSaltFile aliases this.
const SaltBasename = ".idx"

// appWords are plausible standalone macOS app / Application-Support folder names
// (shape A). Real App Support dirs are frequently just the app's display name, so
// one more of these blends in. Deliberately NOT overlapping the launchd-label
// pools (a "find one, grep the rest" pivot gains nothing).
var appWords = []string{
	"Cardhop", "Fantastical", "Fenix", "Bear", "Craft", "Things", "Reeder",
	"Paprika", "Soulver", "Dash", "Kaleidoscope", "Transmit", "Nova", "Yoink",
	"Mimestream", "Timing", "Hazel", "Alfred", "Raycast", "Cleanshot", "Mela",
	"Ivory", "Tot", "Numi", "Downie", "Permute", "Pixelmator", "Acorn",
	"Sketch", "Fork", "Tower", "Sublime", "Zed", "Warp", "Iterm", "Obsidian",
}

// sentinelPool is the DISJOINT pool for the platform-workdir / daemon-home
// content sentinels (disguise_marker.go). Kept separate from markerPool so a
// crypto/rand sentinel basename can never collide with a salt-derived marker
// basename inside the same directory (a collision would overwrite a live marker).
// Neutral, dot-hidden, metadata-looking names an app might drop.
var sentinelPool = []string{
	".DocumentRevisions.plist", ".apdisk", ".TemporaryItems.db",
	".fseventsd.cache", ".metadata_index", ".Trashes.db", ".VolumeIcon.dat",
	".com.apple.timemachine.donotpresent", ".mdimport.state",
	".quicklook.thumb", ".spotlight.shadow", ".caches.index",
}

// pickN returns a uniform crypto/rand int in [0,n). Rejection-sampled so the
// distribution is exactly uniform (a plain modulo of a single byte would bias
// the low residues for n that do not divide 256).
func pickN(n int) int {
	if n <= 1 {
		return 0
	}
	limit := 256 - (256 % n)
	var b [1]byte
	for {
		_, _ = rand.Read(b[:])
		if int(b[0]) < limit {
			return int(b[0]) % n
		}
	}
}

// disguisedDirName produces one disguised directory basename by choosing a SHAPE
// uniformly at random, then filling it from the word pools. Each call samples
// independently, so the daemon-home and every platform-workdir generation read as
// DIFFERENT ordinary apps — no shared prefix, no shared tail, no leading dot, no
// hex. Shapes:
//
//	A  plain app word ............ "Fenix"
//	B  CamelCase vendor+role ..... "LogitechAgent"
//	C  reverse-DNS bundle id ..... "com.brave.helper"   (styleReverseDNS)
//	D  bare vendor word .......... "Grammarly"
func disguisedDirName() string {
	switch pickN(4) {
	case 0:
		return pick(appWords)
	case 1:
		return pick(camelVendors) + pick(camelSuffixes)
	case 2:
		return styleReverseDNS()
	default:
		return pick(camelVendors)
	}
}

// FreshHiddenDir EXCLUSIVELY creates a fresh disguised directory under
// supportRoot and returns its path. It re-rolls the name until os.Mkdir succeeds,
// so it NEVER adopts a pre-existing directory.
//
// This exclusivity is a load-bearing DESTRUCTIVE-SAFETY invariant: a disguised
// name could, by chance, equal a real app-support folder ("Google", "Spotify").
// os.MkdirAll would silently ADOPT that real folder — we would then write our
// content sentinel INTO it, and a later generation sweep (which deletes on a
// positive sentinel match) would RemoveAll the real folder. os.Mkdir fails with
// EEXIST on a collision, so our sentinel only ever lands in a directory we
// exclusively created (guaranteed empty-at-birth) — never a real app's dir.
func FreshHiddenDir(supportRoot string) (string, error) {
	if supportRoot == "" {
		return "", errors.New("relocate: empty supportRoot")
	}
	if err := os.MkdirAll(supportRoot, 0o700); err != nil {
		return "", err
	}
	const maxAttempts = 64
	for i := 0; i < maxAttempts; i++ {
		dir := filepath.Join(supportRoot, disguisedDirName())
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue // collides with a real / existing dir → re-roll (never adopt)
		}
		return "", err
	}
	return "", errors.New("relocate: exhausted attempts creating a fresh disguised dir")
}
