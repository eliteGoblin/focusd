package osadapter

import "testing"

// TestIsFocusdMeshWorkerPlist asserts the FEATURE 19 union predicate used by
// DiscoverAllGenerations to corroborate a real mesh-worker generation across
// the old/new fleet transition:
//   - NEW worker plists carry the env marker MeshEnvKey="run:<role>" → match.
//   - OLD worker plists carry --mesh in argv → match.
//   - the ensure plist (env "ensure", or old `ensure` argv) → NO match
//     (an ensure-only generation is not a real mesh, preserved from FEATURE 17).
//   - a vendor / non-focusd plist (neither marker) → NO match.
func TestIsFocusdMeshWorkerPlist(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		argv []string
		want bool
	}{
		{"new worker A (env run:a)", map[string]string{MeshEnvKey: "run:a"}, []string{"/bin/x"}, true},
		{"new worker B (env run:b)", map[string]string{MeshEnvKey: "run:b"}, []string{"/bin/x"}, true},
		{"old worker (--mesh argv)", nil, []string{"/bin/x", "run", "--r", "a", "--mesh"}, true},
		{"new ensure (env ensure)", map[string]string{MeshEnvKey: "ensure"}, []string{"/bin/x"}, false},
		{"old ensure (ensure argv)", nil, []string{"/bin/x", "ensure"}, false},
		{"vendor plist (neither)", map[string]string{"OTHER": "x"}, []string{"/bin/x", "--foo"}, false},
		{"empty", nil, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isFocusdMeshWorkerPlist(c.env, c.argv); got != c.want {
				t.Fatalf("isFocusdMeshWorkerPlist(%v, %v) = %v, want %v", c.env, c.argv, got, c.want)
			}
		})
	}
}

// TestIsFocusdMeshOrEnsurePlist asserts the DEAD-branch corroboration predicate
// (issue #102-c): unlike the strict worker-only live predicate, it ALSO matches
// the ENSURER — so a dead generation left as only its ensurer plist is swept, not
// stranded. It still excludes a vendor plist (no focusd marker).
func TestIsFocusdMeshOrEnsurePlist(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		argv []string
		want bool
	}{
		{"new worker A (env run:a)", map[string]string{MeshEnvKey: "run:a"}, []string{"/bin/x"}, true},
		{"new worker B (env run:b)", map[string]string{MeshEnvKey: "run:b"}, []string{"/bin/x"}, true},
		{"new ensurer (env ensure) — the #102-c case", map[string]string{MeshEnvKey: "ensure"}, []string{"/bin/x"}, true},
		{"old worker (--mesh argv)", nil, []string{"/bin/x", "run", "--r", "a", "--mesh"}, true},
		{"old/test ensurer (ensure argv)", nil, []string{"/bin/x", "ensure", "--test-mode-flag", "true"}, true},
		{"vendor plist (neither)", map[string]string{"OTHER": "x"}, []string{"/bin/x", "--foo"}, false},
		{"empty", nil, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isFocusdMeshOrEnsurePlist(c.env, c.argv); got != c.want {
				t.Fatalf("isFocusdMeshOrEnsurePlist(%v, %v) = %v, want %v", c.env, c.argv, got, c.want)
			}
		})
	}
}

// TestEncodeRoleExact pins the exact env values the round-trip depends on, so a
// rename of a role string can never silently desync encode/decode.
func TestEncodeRoleExact(t *testing.T) {
	want := map[Role]string{RoleA: "run:a", RoleB: "run:b", RoleEnsure: "ensure"}
	for r, w := range want {
		if got := encodeRole(r); got != w {
			t.Errorf("encodeRole(%s) = %q, want %q", r, got, w)
		}
	}
}
