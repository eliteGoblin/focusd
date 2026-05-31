package status

import (
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/status/redact"
)

func meshLabels() []redact.Token {
	return []redact.Token{
		redact.New("com.apple.metadata.helper.aa11.a"),
		redact.New("com.apple.metadata.helper.aa11.b"),
		redact.New("com.apple.metadata.helper.aa11.ensure"),
	}
}

func TestProbeMesh(t *testing.T) {
	tests := []struct {
		name       string
		loadedN    int  // how many of the 3 labels report loaded
		launchKnow bool
		noLabels   bool
		wantRun    int
		wantKnown  bool
		wantV      Verdict
	}{
		{"all running", 3, true, false, 3, true, Healthy},
		{"partial", 2, true, false, 2, true, Degraded},
		{"none running", 0, true, false, 0, true, Down},
		{"no install", 0, true, true, 0, true, Down},
		{"unknown (no sudo)", 0, false, false, 0, false, Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeSource()
			f.launchKnow = tt.launchKnow
			in := ProbeInput{Domain: "gui/501", MeshLabels: meshLabels()}
			if tt.noLabels {
				in.MeshLabels = nil
			}
			for i, lbl := range meshLabels() {
				if i < tt.loadedN {
					redact.Use(lbl, func(raw string) struct{} {
						f.loaded["gui/501/"+raw] = true
						return struct{}{}
					})
				}
			}
			h := ProbeMesh(f, in)
			if h.RolesRunning != tt.wantRun {
				t.Errorf("RolesRunning = %d, want %d", h.RolesRunning, tt.wantRun)
			}
			if h.Known != tt.wantKnown {
				t.Errorf("Known = %v, want %v", h.Known, tt.wantKnown)
			}
			if h.Verdict != tt.wantV {
				t.Errorf("Verdict = %s, want %s", h.Verdict, tt.wantV)
			}
		})
	}
}

func TestProbePlatform(t *testing.T) {
	wd := redact.New("/wd")
	tests := []struct {
		name    string
		desired string
		good    string
		procs   int
		wantV   Verdict
	}{
		{"healthy", "v1.0.0", "v1.0.0", 1, Healthy},
		{"no proc", "v1.0.0", "v1.0.0", 0, Down},
		{"drift", "v1.1.0", "v1.0.0", 1, Degraded},
		{"fresh no good", "v1.0.0", "", 1, Healthy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeSource()
			f.files["/wd/version.json"] = []byte(`{"desired":"` + tt.desired + `"}`)
			if tt.good != "" {
				f.files["/wd/good"] = []byte(tt.good)
			}
			f.procs["/wd/bin/platform"] = tt.procs
			in := ProbeInput{Workdir: wd, PlatformProcPath: redact.New("/wd/bin/platform")}
			h := ProbePlatform(f, in)
			if h.Desired != tt.desired {
				t.Errorf("Desired = %q, want %q", h.Desired, tt.desired)
			}
			if h.Verdict != tt.wantV {
				t.Errorf("Verdict = %s, want %s", h.Verdict, tt.wantV)
			}
		})
	}
}

func TestProbePluginsUnavailableUnderUser(t *testing.T) {
	f := newFakeSource()
	in := ProbeInput{
		Mode:    "user",
		Workdir: redact.New("/wd"),
		Jobs: []JobSpec{
			{JobID: "dns-block-reconcile", AdminLevel: true},
			{JobID: "skill-protector-reconcile", AdminLevel: false},
		},
	}
	// skill-protector has a fresh ok run; dns-block is admin → unavailable.
	f.runs["skill-protector-reconcile"] = struct {
		run   JobRunInfo
		found bool
	}{JobRunInfo{Status: "ok", StartedAt: f.now.Add(-30 * time.Second)}, true}

	jobs := ProbePlugins(f, in)
	if jobs[0].Verdict != Unavailable {
		t.Errorf("admin job under user install: got %s, want Unavailable", jobs[0].Verdict)
	}
	if jobs[1].Verdict != Healthy {
		t.Errorf("skill job: got %s, want Healthy", jobs[1].Verdict)
	}
	if jobs[1].Age != AgeUnder1m {
		t.Errorf("skill age = %s, want <1m", jobs[1].Age)
	}
}

func TestProbePluginsStatusMapping(t *testing.T) {
	f := newFakeSource()
	in := ProbeInput{Mode: "system", Workdir: redact.New("/wd"),
		Jobs: []JobSpec{{JobID: "j"}}}
	cases := []struct {
		status string
		ageAdd time.Duration
		want   Verdict
	}{
		{"ok", -10 * time.Second, Healthy},
		{"ok", -2 * time.Hour, Degraded}, // stale
		{"failed", -10 * time.Second, Degraded},
		{"error", -10 * time.Second, Degraded},
		{"timedout", -10 * time.Second, Degraded},
	}
	for _, c := range cases {
		f.runs["j"] = struct {
			run   JobRunInfo
			found bool
		}{JobRunInfo{Status: c.status, StartedAt: f.now.Add(c.ageAdd)}, true}
		jobs := ProbePlugins(f, in)
		if jobs[0].Verdict != c.want {
			t.Errorf("status %s age %s → %s, want %s", c.status, c.ageAdd, jobs[0].Verdict, c.want)
		}
	}
}

func TestProbeHosts(t *testing.T) {
	fam := []string{"steampowered.com", "dota2.com"}
	t.Run("present", func(t *testing.T) {
		f := newFakeSource()
		f.files["/etc/hosts"] = []byte("127.0.0.1 localhost\n0.0.0.0 steampowered.com\n0.0.0.0 dota2.com\n")
		h := ProbeHosts(f, ProbeInput{HostsPath: "/etc/hosts", SteamFamily: fam})
		if !h.MarkersPresent || h.BlockedCount != 2 || h.Verdict != Healthy {
			t.Fatalf("got %+v", h)
		}
	})
	t.Run("absent", func(t *testing.T) {
		f := newFakeSource()
		f.files["/etc/hosts"] = []byte("127.0.0.1 localhost\n")
		h := ProbeHosts(f, ProbeInput{HostsPath: "/etc/hosts", SteamFamily: fam})
		if h.MarkersPresent || h.Verdict != Down {
			t.Fatalf("got %+v", h)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		f := newFakeSource()
		h := ProbeHosts(f, ProbeInput{HostsPath: "/etc/hosts", SteamFamily: fam})
		if h.Verdict != Down {
			t.Fatalf("got %+v", h)
		}
	})
}

func TestProbePf(t *testing.T) {
	anchor := redact.New("focusd-block-steam")
	t.Run("disabled", func(t *testing.T) {
		h := ProbePf(newFakeSource(), ProbeInput{PfAnchor: anchor, PfEnabled: false})
		if h.Verdict != Unavailable || !h.Known {
			t.Fatalf("got %+v", h)
		}
	})
	t.Run("enabled with entries", func(t *testing.T) {
		f := newFakeSource()
		f.pfEntries = 42
		f.pfKnown = true
		h := ProbePf(f, ProbeInput{PfAnchor: anchor, PfTable: "steam_ips", PfEnabled: true})
		if h.Entries != 42 || h.Verdict != Healthy {
			t.Fatalf("got %+v", h)
		}
	})
	t.Run("enabled but no sudo", func(t *testing.T) {
		f := newFakeSource()
		f.pfKnown = false
		h := ProbePf(f, ProbeInput{PfAnchor: anchor, PfTable: "steam_ips", PfEnabled: true})
		if h.Known || h.Verdict != Unknown {
			t.Fatalf("got %+v", h)
		}
	})
	t.Run("enabled empty table", func(t *testing.T) {
		f := newFakeSource()
		f.pfEntries = 0
		f.pfKnown = true
		h := ProbePf(f, ProbeInput{PfAnchor: anchor, PfTable: "steam_ips", PfEnabled: true})
		if h.Verdict != Degraded {
			t.Fatalf("got %+v", h)
		}
	})
}

func TestProbeSkills(t *testing.T) {
	paths := []string{"/h/.claude/skills/x/SKILL.md", "/h/.claude/rules/frank/x.md", "/h/.claude/hooks/x.sh"}
	tests := []struct {
		present int
		wantV   Verdict
	}{
		{3, Healthy}, {2, Degraded}, {1, Degraded}, {0, Down},
	}
	for _, tt := range tests {
		f := newFakeSource()
		for i := 0; i < tt.present; i++ {
			f.exists[paths[i]] = true
		}
		h := ProbeSkills(f, ProbeInput{SkillPaths: paths})
		if h.Present != tt.present || h.Verdict != tt.wantV {
			t.Errorf("present %d → %+v, want verdict %s", tt.present, h, tt.wantV)
		}
	}
}
