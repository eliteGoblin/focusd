package main

import (
	"errors"
	"testing"
)

func TestResolveUpdateWorkdir(t *testing.T) {
	const def = "/default/wd"
	discoverOK := func() (string, error) { return "/disguised/wd", nil }
	discoverNone := func() (string, error) { return "", nil }
	discoverErr := func() (string, error) { return "", errors.New("permission denied") }
	yes := func(string) bool { return true }
	no := func(string) bool { return false }

	cases := []struct {
		name     string
		explicit string
		has      func(string) bool
		discover func() (string, error)
		want     string
		wantErr  bool
	}{
		{"explicit override honored even if discoverable", "/x", no, discoverOK, "/x", false},
		{"default with install present uses default", def, yes, discoverOK, def, false},
		{"default empty discovers disguised install", def, no, discoverOK, "/disguised/wd", false},
		{"default empty + no install falls back to default", def, no, discoverNone, def, false},
		{"explicit override honored even with no install", "/x", no, discoverNone, "/x", false},
		{"discovery I/O error fails fast", def, no, discoverErr, "", true},
		{"explicit override skips discovery (no error even if discover would err)", "/x", no, discoverErr, "/x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveUpdateWorkdir(c.explicit, def, c.has, c.discover)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got nil (wd=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("resolveUpdateWorkdir = %q, want %q", got, c.want)
			}
		})
	}
}
