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
	// discoverPanic enforces that discovery is NOT called when an explicit
	// --workdir override is given (it would panic the test if reached).
	discoverPanic := func() (string, error) { panic("discover must not be called for an explicit override") }

	cases := []struct {
		name     string
		explicit string
		discover func() (string, error)
		want     string
		wantErr  bool
	}{
		{"explicit override honored, discovery NOT called", "/x", discoverPanic, "/x", false},
		{"no override: discovered install wins over default", "", discoverOK, "/disguised/wd", false},
		{"no override: discovery finds nothing falls back to default", "", discoverNone, def, false},
		{"no override: discovery I/O error fails fast", "", discoverErr, "", true},
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
