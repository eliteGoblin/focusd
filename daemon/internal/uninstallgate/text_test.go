package uninstallgate

import (
	"math"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Hello   World  ", "hello world"},
		{"line one\nline\ttwo", "line one line two"},
		{"MiXeD\r\nCASE", "mixed case"},
		{"", ""},
		{"   \n\t  ", ""},
		{"already normal", "already normal"},
	}
	for _, c := range cases {
		if got := normalize(c.in); got != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSimilarity(t *testing.T) {
	const ref = "the quick brown fox jumps over the lazy dog"

	if s := similarity(ref, ref); s != 1.0 {
		t.Fatalf("identical → %v, want 1.0", s)
	}
	// Whitespace/case only differences normalize to identical → 1.0.
	if s := similarity("  THE quick\nBROWN  fox jumps over the LAZY dog ", ref); s != 1.0 {
		t.Fatalf("normalized-identical → %v, want 1.0", s)
	}
	// Both empty post-normalize.
	if s := similarity("   ", ""); s != 1.0 {
		t.Fatalf("empty/empty → %v, want 1.0", s)
	}
	// Empty vs non-empty → 0.
	if s := similarity("", ref); s != 0 {
		t.Fatalf("empty vs ref → %v, want 0", s)
	}
	// On a SHORT string a couple of char errors is a large fraction →
	// below threshold (expected; the threshold targets long passages).
	short := strings.Replace(ref, "quick", "qiuck", 1)
	if s := similarity(short, ref); s >= SimilarityThreshold {
		t.Fatalf("typo on short string → %v, expected < %v", s, SimilarityThreshold)
	}

	// Realistic case: a handful of typos across a real ~1800-char
	// passage stays ≥ 0.97 (an honest transcription is accepted).
	long := Passage(1)
	typed := long
	for _, sw := range []struct{ from, to string }{
		{"choosing", "chosing"}, {"protect", "protct"},
		{"permanent", "permanant"}, {"willpower", "will power"},
	} {
		typed = strings.Replace(typed, sw.from, sw.to, 1)
	}
	if s := similarity(typed, long); s < SimilarityThreshold {
		t.Fatalf("few typos over long passage → %v, want ≥ %v", s, SimilarityThreshold)
	}
	// A totally different long input is well below threshold.
	if s := similarity(strings.Repeat("nope ", 300), long); s >= SimilarityThreshold {
		t.Fatalf("different → %v, want < %v", s, SimilarityThreshold)
	}
}

func TestLevenshteinSymmetricAndKnown(t *testing.T) {
	if d := levenshtein([]rune("kitten"), []rune("sitting")); d != 3 {
		t.Fatalf("levenshtein(kitten,sitting) = %d, want 3", d)
	}
	a, b := []rune("flaw"), []rune("lawn")
	if levenshtein(a, b) != levenshtein(b, a) {
		t.Fatal("levenshtein must be symmetric")
	}
	if d := levenshtein([]rune(""), []rune("abc")); d != 3 {
		t.Fatalf("empty vs abc = %d, want 3", d)
	}
}

func TestSimilarityBoundedRatio(t *testing.T) {
	if s := similarity("abcdefghij", "klmnopqrst"); s < 0 || s > 1 || math.IsNaN(s) {
		t.Fatalf("ratio out of [0,1]: %v", s)
	}
}
