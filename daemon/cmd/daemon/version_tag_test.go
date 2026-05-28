package main

import "testing"

func TestIsValidVersionTag(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// happy path
		{"v0.0.1", true},
		{"v0.9.0", true},
		{"v1.2.3", true},
		{"v10.20.30", true},
		{"v1.2.3-rc.1", true},
		{"v1.2.3-beta-foo", true},
		{"v1.2.3+abc123", true},
		{"v1.2.3-rc.1+build.42", true},

		// rejected: missing "v"
		{"1.2.3", false},
		{"", false},

		// rejected: not strict semver
		{"v1", false},
		{"v1.2", false},
		{"vlatest", false},
		{"v1.2.x", false},

		// rejected: path traversal / separators (the Copilot concern)
		{"v/../etc/passwd", false},
		{"v..", false},
		{"v0.1.0/extra", false},
		{"v0.1.0\\x", false},
		{"v0.1.0 ", false},
		{" v0.1.0", false},
		{"v0.1.0\n", false},

		// rejected: dangerous pre-release shapes
		{"v1.2.3-", false},
		{"v1.2.3-/x", false},
		{"v1.2.3-../x", false},
	}
	for _, c := range cases {
		got := isValidVersionTag(c.in)
		if got != c.want {
			t.Errorf("isValidVersionTag(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
