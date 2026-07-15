package relocate

// FEATURE 26 — de-pattern the per-install MARKER basenames.
//
// The daemon-home holds several small state files whose basenames were fixed
// cross-install literals: the masked roster (".roster"), the liveness pidfile
// (".seq"), the platform-workdir pointer, and the singleton lock
// ("platform.lock"). Any one of them made the whole install findable with a
// single `find ~/Library -name .roster` (or -name platform.lock) — a fixed
// signature that spans every machine.
//
// Each such basename is now DERIVED from the per-install salt: same salt → same
// basenames (every daemon role + the status subcommand reconstruct them), but a
// DIFFERENT install picks different ones, so no fixed literal spans machines. The
// derivation is a distinct assignment (no two purposes collide within an install)
// drawn from a neutral, metadata-looking pool.
//
// Irreducible residual (accepted, simplest): the salt file's OWN basename stays a
// fixed literal ([SaltBasename]) — a reader needs one stable name to bootstrap
// the salt before it can derive the rest.

import (
	"encoding/binary"
	"strconv"
)

// markerPool is the pool of neutral, dot-hidden basenames the salt-derived
// markers draw from. Metadata-looking so a casual `ls -a` in the (already
// hidden) daemon-home does not flag them, and generic enough that a full-disk
// `find -name` hit reveals nothing product-named. DISJOINT from sentinelPool so a
// salt-derived marker and a crypto/rand sentinel never collide in one directory.
var markerPool = []string{
	".cache.db", ".state.dat", ".prefs.plist", ".index.bin", ".store.idx",
	".session.dat", ".registry.plist", ".manifest.bin", ".config.idx", ".journal.dat",
	".catalog.plist", ".snapshot.bin", ".envelope.idx", ".ledger.dat", ".digest.plist",
	".segment.bin", ".profile.idx", ".shard.dat", ".metrics.plist", ".trace.bin",
	".spool.idx", ".record.dat", ".keyring.plist", ".anchor.bin", ".depot.idx",
	".fragment.dat", ".volume.plist", ".channel.bin", ".cluster.idx", ".matrix.dat",
}

// markerPurposes is the FIXED, ORDERED list of salt-derived marker slots. Order
// is load-bearing: the distinct assignment maps purpose i → the i-th entry of the
// salt-seeded pool permutation, so every reader must agree on this order to
// reconstruct the same basenames. Append-only (never reorder) to keep an existing
// install's basenames stable across a binary upgrade.
var markerPurposes = []string{"roster", "pidfile", "pointer", "lock"}

// markerAssignment returns the per-install purpose→basename map: a distinct entry
// from markerPool for each purpose, so no two markers in a daemon-home ever share
// a basename (which would overwrite a live marker). It permutes the pool indices
// deterministically from the salt (a sha256 keystream drives a Fisher-Yates
// shuffle) and hands out the first len(markerPurposes) entries. Requires
// len(markerPool) >= len(markerPurposes).
func markerAssignment(salt string) map[string]string {
	perm := make([]int, len(markerPool))
	for i := range perm {
		perm[i] = i
	}
	// Fisher-Yates driven by a salt-keyed sha256 keystream. Each step consumes 4
	// bytes; re-hash (salt|"markerperm"|counter) whenever the block is exhausted.
	var block [32]byte
	off := len(block) // force an initial hash
	ctr := 0
	nextRand := func() uint32 {
		if off+4 > len(block) {
			block = hashSeed(salt + "|markerperm|" + strconv.Itoa(ctr))
			ctr++
			off = 0
		}
		v := binary.BigEndian.Uint32(block[off : off+4])
		off += 4
		return v
	}
	for i := len(perm) - 1; i > 0; i-- {
		j := int(nextRand() % uint32(i+1))
		perm[i], perm[j] = perm[j], perm[i]
	}
	out := make(map[string]string, len(markerPurposes))
	for i, p := range markerPurposes {
		out[p] = markerPool[perm[i]]
	}
	return out
}

// MarkerBasename returns the disguised basename for a salt-derived marker
// (purpose one of markerPurposes), or "" when salt is empty (dev/test/legacy: the
// caller falls back to the fixed legacy literal) or the purpose is unknown.
func MarkerBasename(salt, purpose string) string {
	if salt == "" {
		return ""
	}
	return markerAssignment(salt)[purpose]
}
