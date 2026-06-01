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

	cases := []struct {
		name     string
		explicit string
		discover func() (string, error)
		want     string
		wantErr  bool
	}{
		{"explicit override honored, skips discovery", "/x", discoverOK, "/x", false},
		{"discovered real install shadows the stale default", def, discoverOK, "/disguised/wd", false},
		{"default + discovery finds nothing falls back to default", def, discoverNone, def, false},
		{"discovery I/O error fails fast", def, discoverErr, "", true},
		{"explicit override skips discovery even when discovery would error", "/x", discoverErr, "/x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveUpdateWorkdir(c.explicit, def, c.discover)
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
