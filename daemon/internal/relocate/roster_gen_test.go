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

// TestGenerateRosterThreeDistinctFamilies asserts acceptance #1:
// 3 labels, drawn from 3 DIFFERENT vendor families, no shared prefix/stem,
// no .a/.b/.ensure (or any) role token, each well-formed.
func TestGenerateRosterThreeDistinctFamilies(t *testing.T) {
	shape := regexp.MustCompile(`^[a-zA-Z0-9._-]+\.[a-z]+\.[0-9a-f]{10}$`)
	roleTokens := []string{".a", ".b", ".ensure"}

	for iter := 0; iter < 200; iter++ {
		roster := GenerateRoster()
		if len(roster) != 3 {
			t.Fatalf("roster must have 3 labels, got %d: %v", len(roster), roster)
		}
		fams := map[string]struct{}{}
		for _, label := range roster {
			if strings.Contains(label, "focusd") {
				t.Fatalf("label leaks project string: %s", label)
			}
			if !shape.MatchString(label) {
				t.Fatalf("label not well-formed: %s", label)
			}
			for _, tok := range roleTokens {
				if strings.HasSuffix(label, tok) {
					t.Fatalf("label carries role token %q: %s", tok, label)
				}
			}
			fams[family(label)] = struct{}{}
		}
		if len(fams) != 3 {
			t.Fatalf("roster families not distinct: %v (families %v)", roster, fams)
		}
	}
}

// TestGenerateRosterNoSharedStem asserts the three labels do not all
// share a common prefix segment (the cluster-find weakness F10 closes):
// no two labels share the same family.
func TestGenerateRosterNoSharedStem(t *testing.T) {
	roster := GenerateRoster()
	seen := map[string]struct{}{}
	for _, label := range roster {
		f := family(label)
		if _, dup := seen[f]; dup {
			t.Fatalf("two labels share family %q: %v", f, roster)
		}
		seen[f] = struct{}{}
	}
}
