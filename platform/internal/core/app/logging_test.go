package app

import (
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/plugin"
)

// TestRejectedID returns the manifest id when present, else the dir base,
// never the full disguised workdir path.
func TestRejectedID(t *testing.T) {
	cases := []struct {
		name string
		p    plugin.Discovered
		want string
	}{
		{
			name: "manifest id present",
			p:    plugin.Discovered{Manifest: &plugin.Manifest{ID: "kill-steam"}, Dir: "/disguised/work/dir/kill-steam"},
			want: "kill-steam",
		},
		{
			name: "nil manifest falls back to dir base",
			p:    plugin.Discovered{Dir: "/disguised/work/dir/network-block"},
			want: "network-block",
		},
		{
			name: "nothing known",
			p:    plugin.Discovered{},
			want: "unknown",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rejectedID(c.p); got != c.want {
				t.Errorf("rejectedID = %q, want %q", got, c.want)
			}
		})
	}
}

// TestRedactPaths scrubs path-like tokens but preserves the diagnostic words.
func TestRedactPaths(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "io error embeds a path",
			in:   "read plugin.json: open /disguised/work/dir/p/plugin.json: no such file",
			want: "read plugin.json: open <redacted> no such file",
		},
		{
			name: "windows path token is scrubbed",
			in:   `open C:\disguised\work\dir\p\plugin.json: not found`,
			want: "open <redacted> not found",
		},
		{
			name: "path-free reason is untouched in meaning",
			in:   `unsupported protocol_version "9"`,
			want: `unsupported protocol_version "9"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redactPaths(c.in)
			if got != c.want {
				t.Errorf("redactPaths = %q, want %q", got, c.want)
			}
			if strings.ContainsRune(got, '/') || strings.ContainsRune(got, '\\') {
				t.Errorf("redactPaths left a path separator in %q", got)
			}
		})
	}
}
