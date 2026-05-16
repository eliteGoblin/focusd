package reconcile

import (
	"strconv"
	"strings"
)

// DefaultVersionCompare compares versions like "v1", "v2", "1.4.0",
// "v1.10.2". It compares dot-separated numeric segments left to right;
// a missing segment counts as 0. If a segment is non-numeric it falls
// back to a plain string comparison of the whole input (safe, total
// order, never panics).
func DefaultVersionCompare(a, b string) int {
	as := segments(a)
	bs := segments(b)
	if as == nil || bs == nil {
		return strings.Compare(a, b)
	}
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// segments parses "v1.10.2" → [1 10 2]. Returns nil if any segment is
// not a non-negative integer (signals the caller to string-compare).
func segments(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}
