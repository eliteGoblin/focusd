package main

import "testing"

func TestResolveUpdateWorkdir(t *testing.T) {
	const def = "/default/wd"
	discoverOK := func() (string, bool) { return "/disguised/wd", true }
	discoverNone := func() (string, bool) { return "", false }
	yes := func(string) bool { return true }
	no := func(string) bool { return false }

	cases := []struct {
		name     string
		explicit string
		has      func(string) bool
		discover func() (string, bool)
		want     string
	}{
		{"explicit override is honored even if discoverable", "/x", no, discoverOK, "/x"},
		{"default with install present uses default", def, yes, discoverOK, def},
		{"default empty discovers disguised install", def, no, discoverOK, "/disguised/wd"},
		{"default empty + discovery fails falls back to default", def, no, discoverNone, def},
		{"explicit override honored even when it has no install", "/x", no, discoverNone, "/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveUpdateWorkdir(c.explicit, def, c.has, c.discover)
			if got != c.want {
				t.Fatalf("resolveUpdateWorkdir = %q, want %q", got, c.want)
			}
		})
	}
}
