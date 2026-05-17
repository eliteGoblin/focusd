package uninstallgate

import (
	"strings"
	"unicode"
)

// normalize makes transcription comparison forgiving of the things a
// careful human gets "wrong" without skipping effort: case, and any
// run/kind of whitespace (newlines vs spaces, double spaces, trailing
// space). It deliberately does NOT drop punctuation or words — the user
// must still transcribe the actual content.
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// similarity returns a [0,1] ratio = 1 - levenshtein(a,b)/max(len). Two
// identical (post-normalize) strings → 1.0; totally different → ~0. Used
// so an honest typo or two over a 250-word passage does not destroy the
// effort, while a substantially wrong/empty transcription is rejected.
func similarity(a, b string) float64 {
	a, b = normalize(a), normalize(b)
	if a == b {
		return 1
	}
	ra, rb := []rune(a), []rune(b)
	d := levenshtein(ra, rb)
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}
	if maxLen == 0 {
		return 1 // both empty post-normalize
	}
	return 1 - float64(d)/float64(maxLen)
}

// levenshtein is the classic two-row edit distance (O(n*m) time, O(min)
// space). Inputs here are a few thousand runes — well within budget.
func levenshtein(a, b []rune) int {
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
