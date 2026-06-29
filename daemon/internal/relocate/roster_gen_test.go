package relocate

import (
	"regexp"
	"strings"
	"testing"
)

// TestFamilyExtractsOrgSegment asserts family() returns the first two
// dot-segments (the org/vendor segment) and handles 2-segment entries.
func TestFamilyExtractsOrgSegment(t *testing.T) {
	cases := map[string]string{
		"com.apple.metadata":                "com.apple",
		"com.google.keystone.daemon":        "com.google",
		"org.mozilla.updater":               "org.mozilla",
		"us.zoom.ZoomDaemon":                "us.zoom",
		"io.tailscale.ipnextension":         "io.tailscale",
		"notion.id.helper":                  "notion.id",
		"company.thebrowser.Browser.helper": "company.thebrowser",
		// 2-segment safety: returned whole.
		"single":    "single",
		"two.parts": "two.parts",
	}
	for in, want := range cases {
		if got := family(in); got != want {
			t.Errorf("family(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPrefixesPoolHasAtLeastThreeFamilies guards the generator's
// precondition: sampling 3 DISTINCT families without replacement is only
// possible if the prefixes pool actually maps to >= 3 vendor families.
func TestPrefixesPoolHasAtLeastThreeFamilies(t *testing.T) {
	fams := map[string]struct{}{}
	for _, p := range prefixes {
		fams[family(p)] = struct{}{}
	}
	if len(fams) < 3 {
		t.Fatalf("prefixes pool maps to %d families, need >= 3 for distinct-family roster", len(fams))
	}
}

// filenameSafe matches the label charset that is also a valid plist filename
// stem (label == filepath.Base(plist) in bootstrap()): ASCII letters, digits,
// dot, underscore, hyphen — no slashes, spaces, or colons.
var filenameSafe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// TestGenerateRosterDistinctStyles asserts FEATURE 19 acceptance #1+#2: the
// three labels do NOT read as a matching set. Each role gets a STRUCTURALLY
// DISTINCT style:
//   - role A → dotted reverse-DNS (has dots, no CamelCase, no trailing-d shape)
//   - role B → CamelCase (no dots, starts uppercase)
//   - role ensure → lowercase unix-daemon (no dots, lowercase, trailing 'd')
//
// None carry a role token (.a/.b/.ensure) or "focusd"; all are filename-safe.
func TestGenerateRosterDistinctStyles(t *testing.T) {
	roleTokens := []string{".a", ".b", ".ensure"}
	for iter := 0; iter < 500; iter++ {
		roster := GenerateRoster()
		if len(roster) != 3 {
			t.Fatalf("roster must have 3 labels, got %d: %v", len(roster), roster)
		}
		a, b, ens := roster[0], roster[1], roster[2]

		for _, label := range roster {
			if strings.Contains(label, "focusd") {
				t.Fatalf("label leaks project string: %s", label)
			}
			if !filenameSafe.MatchString(label) {
				t.Fatalf("label not filename-safe: %q", label)
			}
			for _, tok := range roleTokens {
				if strings.HasSuffix(label, tok) {
					t.Fatalf("label carries role token %q: %s", tok, label)
				}
			}
		}

		// role A: reverse-DNS → contains dots, all lowercase-ish (no uppercase).
		if !strings.Contains(a, ".") {
			t.Fatalf("role A must be dotted reverse-DNS: %q", a)
		}
		if a != strings.ToLower(a) {
			t.Fatalf("role A reverse-DNS must be lowercase: %q", a)
		}
		// role B: CamelCase → NO dots, starts with an uppercase letter.
		if strings.Contains(b, ".") {
			t.Fatalf("role B CamelCase must have no dots: %q", b)
		}
		if b[0] < 'A' || b[0] > 'Z' {
			t.Fatalf("role B CamelCase must start uppercase: %q", b)
		}
		// role ensure: daemon-ish → NO dots, lowercase, ends in 'd'.
		if strings.Contains(ens, ".") {
			t.Fatalf("role ensure daemon name must have no dots: %q", ens)
		}
		if ens != strings.ToLower(ens) || !strings.HasSuffix(ens, "d") {
			t.Fatalf("role ensure must be a lowercase trailing-d daemon name: %q", ens)
		}

		// The three must not share a visible stem: no two are equal, and they
		// have three different shapes (asserted above), so they can't cluster.
		if a == b || b == ens || a == ens {
			t.Fatalf("roster labels must be distinct: %v", roster)
		}
	}
}

// TestBinaryNamePoolDisjointFromLabelPools asserts FEATURE 19: the daemon binary
// basename draws from its OWN word pool (binWords), disjoint from every mesh
// label pool, so the binary shares no stem/word with any launchd label.
func TestBinaryNamePoolDisjointFromLabelPools(t *testing.T) {
	labelPools := [][]string{dnsRoots, dnsLeaves, camelVendors, camelMids, camelSuffixes, daemonRoots}
	for _, w := range binWords {
		for _, pool := range labelPools {
			for _, p := range pool {
				if strings.EqualFold(w, p) {
					t.Fatalf("binWords token %q overlaps a label pool token %q", w, p)
				}
			}
		}
	}
}
