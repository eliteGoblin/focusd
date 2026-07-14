package relocate

import (
	"strings"
	"testing"
)

// shapeSig is a coarse structural fingerprint of a launchd label: whether it has
// dots (reverse-DNS), starts uppercase (CamelCase), and ends in 'd' (unix daemon).
// Two labels that CLUSTER (the "3 look the same" tell the owner spotted) share a
// shape; HF4 (FEATURE 24) requirement D demands the mesh labels be mutually
// dissimilar, so the three shapes must all differ.
func shapeSig(label string) string {
	hasDot := strings.Contains(label, ".")
	upperStart := len(label) > 0 && label[0] >= 'A' && label[0] <= 'Z'
	endsD := strings.HasSuffix(label, "d")
	return map[bool]string{true: "D", false: "-"}[hasDot] +
		map[bool]string{true: "U", false: "-"}[upperStart] +
		map[bool]string{true: "d", false: "-"}[endsD]
}

// lcp returns the length of the longest common prefix of a and b.
func lcp(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// TestMeshLabelsMutuallyDissimilar is the HF4 (FEATURE 24) requirement-D guard:
// the 2–3 mesh launchd labels must NOT share a pattern — no "xx.xx.xx" cluster,
// no 3-consecutive-similar tell. Each label is independently randomized to look
// like a DISTINCT, unrelated real vendor item. We assert, over many rosters:
//   - all three structural SHAPES differ (no two read as the same kind of item);
//   - no two labels share a long common prefix (no "com.apple.X / com.apple.Y"
//     sibling tell);
//   - the three are pairwise distinct strings.
func TestMeshLabelsMutuallyDissimilar(t *testing.T) {
	const maxSharedPrefix = 5 // "com." (4) is the most any pair may incidentally share
	for iter := 0; iter < 1000; iter++ {
		roster := GenerateRoster()
		if len(roster) != 3 {
			t.Fatalf("roster must have 3 labels, got %v", roster)
		}

		// 1. Distinct SHAPES — the anti-clustering core. Three labels ⇒ three
		// different structural signatures (no matching set).
		sigs := map[string]int{}
		for _, l := range roster {
			sigs[shapeSig(l)]++
		}
		if len(sigs) != 3 {
			t.Fatalf("mesh labels share a structural shape (cluster tell) %v → sigs %v", roster, sigs)
		}

		// 2. No long shared prefix between any pair (no reverse-DNS sibling tell).
		for i := 0; i < len(roster); i++ {
			for j := i + 1; j < len(roster); j++ {
				if p := lcp(roster[i], roster[j]); p > maxSharedPrefix {
					t.Fatalf("labels %q and %q share a %d-char prefix (>%d) — clustering tell",
						roster[i], roster[j], p, maxSharedPrefix)
				}
				if roster[i] == roster[j] {
					t.Fatalf("labels not distinct: %v", roster)
				}
			}
		}
	}
}

// TestMeshLabelLengthsVary sanity-checks that the three roles don't all land on a
// rigid fixed-width template (another way three items "look the same"). Over a
// sample the three roles' lengths should not be identical every time.
func TestMeshLabelLengthsVary(t *testing.T) {
	sawDistinctLengths := false
	for iter := 0; iter < 200 && !sawDistinctLengths; iter++ {
		r := GenerateRoster()
		if len(r[0]) != len(r[1]) || len(r[1]) != len(r[2]) {
			sawDistinctLengths = true
		}
	}
	if !sawDistinctLengths {
		t.Error("every sampled roster had three equal-length labels — looks templated")
	}
}
