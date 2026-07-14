package relocate

import (
	"regexp"
	"strings"
	"testing"
)

// TestPlatformTokensDeterministicPerSalt: the disguise tokens MUST be a pure
// function of (salt, version) so the daemon that starts the platform and the
// status subcommand that greps for it derive the identical argv/path with no
// shared lookup table. Same salt → same tokens; different salt → (almost surely)
// different tokens.
func TestPlatformTokensDeterministicPerSalt(t *testing.T) {
	const s1, s2 = "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"
	if PlatformArgv0(s1) != PlatformArgv0(s1) {
		t.Error("PlatformArgv0 not deterministic for a fixed salt")
	}
	if PlatformBinBase(s1, "v1") != PlatformBinBase(s1, "v1") {
		t.Error("PlatformBinBase not deterministic for a fixed salt+version")
	}
	// Different versions under the same salt map to different bases (so a
	// re-fetch of a new version doesn't collide on the prior version's path).
	if PlatformBinBase(s1, "v1") == PlatformBinBase(s1, "v2") {
		t.Error("distinct versions must map to distinct binary bases")
	}
	// A different install (salt) yields different disguise, defeating a
	// cross-machine grep pivot.
	if PlatformArgv0(s1) == PlatformArgv0(s2) && PlatformBinBase(s1, "v1") == PlatformBinBase(s2, "v1") {
		t.Error("distinct salts produced identical disguise (argv0 AND bin base)")
	}
}

// TestPlatformTokensEmptySaltIsLegacy: no salt ⇒ empty tokens, so callers fall
// back to the legacy, non-disguised layout (dev/test/e2e stay unchanged).
func TestPlatformTokensEmptySaltIsLegacy(t *testing.T) {
	if PlatformArgv0("") != "" || PlatformBinBase("", "v1") != "" {
		t.Error("empty salt must yield empty tokens (legacy fallback)")
	}
}

// TestPlatformTokensCarryNoLeak: neither token may contain a greppable install
// token — no 'platform', 'focusd', 'daemon', no version, no path separator.
func TestPlatformTokensCarryNoLeak(t *testing.T) {
	banned := []string{"platform", "focusd", "daemon", "workdir", "v0.", "/", "\\", " "}
	for _, v := range []string{"v0.16.7", "v1.2.3", "v0.5.10"} {
		toks := []string{PlatformArgv0("saltsaltsalt1234"), PlatformBinBase("saltsaltsalt1234", v)}
		for _, tok := range toks {
			low := strings.ToLower(tok)
			for _, b := range banned {
				if strings.Contains(low, b) {
					t.Errorf("token %q leaks banned substring %q", tok, b)
				}
			}
		}
	}
}

// TestPlatformBinBaseShape: "<word>.<10-hex>" — a plausible on-disk data file, no
// version, no 'platform'.
func TestPlatformBinBaseShape(t *testing.T) {
	base := PlatformBinBase("saltsaltsalt1234", "v0.16.7")
	if !regexp.MustCompile(`^[a-z]+\.[0-9a-f]{10}$`).MatchString(base) {
		t.Errorf("bin base %q is not <word>.<10-hex>", base)
	}
}

// TestPlatformPoolsDisjoint: the platform pools must not overlap the daemon
// binary pool (binWords) — "find one, grep for the rest" must gain nothing.
func TestPlatformPoolsDisjoint(t *testing.T) {
	bin := map[string]bool{}
	for _, w := range binWords {
		bin[w] = true
	}
	for _, w := range procTokens {
		if bin[w] {
			t.Errorf("procTokens shares %q with the daemon binWords pool", w)
		}
	}
	for _, w := range platBinWords {
		if bin[w] {
			t.Errorf("platBinWords shares %q with the daemon binWords pool", w)
		}
	}
}
