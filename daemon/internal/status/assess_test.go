package status

import "testing"

func TestAssess_Verdicts(t *testing.T) {
	cases := []struct {
		name string
		in   Snapshot
		want Verdict
	}{
		{
			name: "healthy: full mesh, proc up, no drift",
			in:   Snapshot{Found: true, MeshLoaded: 3, MeshTotal: 3, ProcCount: 1, Desired: "v1", Good: "v1"},
			want: Healthy,
		},
		{
			name: "healthy: desired==good with no proc requirement met",
			in:   Snapshot{Found: true, MeshLoaded: 2, MeshTotal: 2, ProcCount: 1, Desired: "v2", Good: "v2"},
			want: Healthy,
		},
		{
			name: "warming up is healthy, not down",
			in:   Snapshot{Found: true, MeshLoaded: 3, MeshTotal: 3, WarmingUp: true, Good: "", ProcCount: 0},
			want: Healthy,
		},
		{
			name: "no install found is a clean DOWN",
			in:   Snapshot{Found: false, MeshTotal: 0},
			want: Down,
		},
		{
			name: "genuine mesh down: found but zero roles loaded",
			in:   Snapshot{Found: true, MeshLoaded: 0, MeshTotal: 3, Good: "v1", ProcCount: 1},
			want: Down,
		},
		{
			name: "good version present but process gone is DOWN",
			in:   Snapshot{Found: true, MeshLoaded: 3, MeshTotal: 3, Good: "v1", Desired: "v1", ProcCount: 0},
			want: Down,
		},
		{
			name: "partial mesh is DEGRADED",
			in:   Snapshot{Found: true, MeshLoaded: 2, MeshTotal: 3, Good: "v1", Desired: "v1", ProcCount: 1},
			want: Degraded,
		},
		{
			name: "version drift is DEGRADED",
			in:   Snapshot{Found: true, MeshLoaded: 3, MeshTotal: 3, Good: "v1", Desired: "v2", ProcCount: 1},
			want: Degraded,
		},
		{
			name: "more than one platform process is DEGRADED anomaly",
			in:   Snapshot{Found: true, MeshLoaded: 3, MeshTotal: 3, Good: "v1", Desired: "v1", ProcCount: 2},
			want: Degraded,
		},
		{
			name: "mesh unknown only (system without sudo) is UNKNOWN, not down/degraded",
			in:   Snapshot{Found: true, MeshUnknown: true, VersionsUnknown: true},
			want: Unknown,
		},
		{
			name: "versions unknown only is UNKNOWN",
			in:   Snapshot{Found: true, MeshLoaded: 3, MeshTotal: 3, VersionsUnknown: true},
			want: Unknown,
		},
		{
			name: "all-unknown system read must NOT upgrade to degraded/down",
			in:   Snapshot{Mode: "system", Found: true, MeshUnknown: true, VersionsUnknown: true},
			want: Unknown,
		},
		{
			name: "proc anomaly with versions unknown stays UNKNOWN (not degraded)",
			in:   Snapshot{Found: true, MeshLoaded: 3, MeshTotal: 3, VersionsUnknown: true, ProcCount: 2},
			want: Unknown,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Assess(c.in).Verdict
			if got != c.want {
				t.Fatalf("Assess(%+v) verdict = %s; want %s", c.in, got, c.want)
			}
		})
	}
}

// TestAssess_PrecedenceUnknownNeverUpgrades pins the load-bearing rule: an
// unknown read must never be escalated. Even with a mesh count that looks
// partial, MeshUnknown shadows it and the verdict stays Unknown (we cannot
// trust counts we couldn't actually read).
func TestAssess_UnknownShadowsPartialCounts(t *testing.T) {
	in := Snapshot{Found: true, MeshUnknown: true, MeshLoaded: 1, MeshTotal: 3, VersionsUnknown: true}
	if got := Assess(in).Verdict; got != Unknown {
		t.Fatalf("verdict = %s; want %s (mesh-unknown must not read as partial/degraded)", got, Unknown)
	}
}

func TestExitCode(t *testing.T) {
	cases := map[Verdict]int{
		Healthy:  0,
		Unknown:  0, // unknown folds into healthy-for-exit
		Degraded: 1,
		Down:     2,
	}
	for v, want := range cases {
		if got := ExitCode(v); got != want {
			t.Errorf("ExitCode(%s) = %d; want %d", v, got, want)
		}
	}
}
