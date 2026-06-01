package status

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// forbidden are substrings that would indicate a disguised identifier leaked
// into status output: filesystem path roots, launchd plist/label shapes, the
// pf anchor literal. The redaction contract (ADR-0011) says NONE of these may
// appear in the daemon's OWN rendered output, on any path.
var forbidden = []string{
	"/Library/",
	"/Users/",
	"/var/",
	"Application Support",
	".plist",
	"com.apple",
	"com.brave", // non-apple vendor prefix (relocate pool is mostly non-apple)
	"anchor",
	"LaunchAgents",
	"LaunchDaemons",
}

// realisticSnapshot is a fully-populated daemon snapshot. Crucially it holds
// ONLY primitives — by construction it cannot carry a disguised path, because
// the type has no field for one. This test guards that the renderer doesn't
// introduce one.
func realisticSnapshot() Snapshot {
	return Snapshot{
		Mode:       "system",
		MeshLoaded: 2,
		MeshTotal:  3,
		ProcCount:  1,
		Desired:    "v1.2.0",
		Good:       "v1.1.0",
		Found:      true,
	}
}

func assertNoLeak(t *testing.T, label, out string) {
	t.Helper()
	for _, bad := range forbidden {
		if strings.Contains(out, bad) {
			t.Errorf("%s leaked forbidden substring %q:\n%s", label, bad, out)
		}
	}
}

func TestRender_NoLeak_CleanSnapshot(t *testing.T) {
	s := realisticSnapshot()
	res := Assess(s)

	var txt bytes.Buffer
	RenderText(s, res, PlatformDetail{Available: false}, &txt, false)
	assertNoLeak(t, "text", txt.String())

	var js bytes.Buffer
	RenderJSON(s, res, PlatformDetail{Available: false}, &js)
	assertNoLeak(t, "json", js.String())
}

// TestRender_PoisonedPlatformDetail feeds the renderer a platform passthrough
// blob containing real-looking disguised paths. The daemon passes platform
// output through (the platform owns its own redaction, ADR-0012), so the
// passthrough text MAY legitimately appear. The contract the daemon enforces
// is that the daemon's OWN fields never leak. We verify that the daemon does
// not ADD any forbidden substring beyond what the (poisoned) platform blob
// itself contained — i.e. the daemon section + overall line are clean.
func TestRender_DaemonSectionCleanDespitePoisonedPassthrough(t *testing.T) {
	s := realisticSnapshot()
	res := Assess(s)

	poison := "  net-block  /Library/LaunchDaemons/com.apple.x.plist anchor=focusd\n"

	var txt bytes.Buffer
	RenderText(s, res, PlatformDetail{Available: true, TextOutput: poison}, &txt, false)

	full := txt.String()
	// The poison line is present (verbatim passthrough). Remove exactly the
	// passthrough block and assert what remains — the daemon's own output —
	// is clean.
	daemonOwn := strings.Replace(full, poison, "", 1)
	assertNoLeak(t, "daemon-own text (poison removed)", daemonOwn)
}

// TestRenderJSON_StructuralComposition verifies the JSON is a single valid
// document with a nested platform value — never two concatenated objects.
func TestRenderJSON_StructuralComposition(t *testing.T) {
	s := realisticSnapshot()
	res := Assess(s)

	platformReport := json.RawMessage(`{"mode":"system","jobs":[],"overall":"HEALTHY"}`)

	var buf bytes.Buffer
	RenderJSON(s, res, PlatformDetail{Available: true, JSON: platformReport}, &buf)

	var top map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &top); err != nil {
		t.Fatalf("combined JSON is not a single valid object: %v\n%s", err, buf.String())
	}
	for _, k := range []string{"daemon", "platform", "platform_status", "overall"} {
		if _, ok := top[k]; !ok {
			t.Errorf("combined JSON missing key %q", k)
		}
	}
	// platform must be the nested object, embedded structurally.
	var pm map[string]any
	if err := json.Unmarshal(top["platform"], &pm); err != nil {
		t.Fatalf("platform value is not a nested object: %v", err)
	}
	if pm["overall"] != "HEALTHY" {
		t.Errorf("nested platform overall = %v; want HEALTHY", pm["overall"])
	}
	if got := strings.Trim(string(top["platform_status"]), `"`); got != "ok" {
		t.Errorf("platform_status = %q; want ok", got)
	}
}

// TestRenderJSON_DaemonSectionCleanDespitePoisonedPlatformJSON feeds a
// platform JSON blob laced with forbidden substrings (a fake path + a
// com.brave label) as pd.JSON. The daemon passes platform output through
// structurally (ADR-0012 — the platform owns its own redaction), so the
// embedded platform value MAY carry those. The contract the daemon enforces
// is that its OWN fields stay clean. We extract just the "daemon" object,
// re-marshal it, and assert no leak against the daemon-only bytes.
func TestRenderJSON_DaemonSectionCleanDespitePoisonedPlatformJSON(t *testing.T) {
	s := realisticSnapshot()
	res := Assess(s)

	poisoned := json.RawMessage(
		`{"overall":"DOWN","note":"/Users/x/Library/LaunchDaemons/com.brave.y.plist anchor=focusd"}`)

	var buf bytes.Buffer
	RenderJSON(s, res, PlatformDetail{Available: true, JSON: poisoned}, &buf)

	var top map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &top); err != nil {
		t.Fatalf("combined JSON is not a single valid object: %v\n%s", err, buf.String())
	}
	daemonBytes, ok := top["daemon"]
	if !ok {
		t.Fatalf("combined JSON missing daemon object:\n%s", buf.String())
	}
	// Re-marshal the daemon object alone so the platform passthrough cannot
	// contaminate the assertion; the daemon's own fields must be clean.
	var daemonObj map[string]any
	if err := json.Unmarshal(daemonBytes, &daemonObj); err != nil {
		t.Fatalf("daemon value is not an object: %v", err)
	}
	clean, err := json.Marshal(daemonObj)
	if err != nil {
		t.Fatalf("re-marshal daemon object: %v", err)
	}
	assertNoLeak(t, "daemon-only JSON (platform poisoned)", string(clean))
}

// TestRenderJSON_PlatformUnavailable: when platform detail is unavailable,
// platform is JSON null and platform_status is "unavailable".
func TestRenderJSON_PlatformUnavailable(t *testing.T) {
	s := realisticSnapshot()
	res := Assess(s)

	var buf bytes.Buffer
	RenderJSON(s, res, PlatformDetail{Available: false}, &buf)

	var top map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &top); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if string(top["platform"]) != "null" {
		t.Errorf("platform = %s; want null", top["platform"])
	}
	if got := strings.Trim(string(top["platform_status"]), `"`); got != "unavailable" {
		t.Errorf("platform_status = %q; want unavailable", got)
	}
}

// TestPlatformDetail_Verdict pins the platform-verdict derivation that BUG 2
// folds into the combined OVERALL: JSON "overall" is authoritative when
// present, exit code is the fallback, and an unavailable detail returns ok=false
// (do not fold).
func TestPlatformDetail_Verdict(t *testing.T) {
	cases := []struct {
		name   string
		pd     PlatformDetail
		want   Verdict
		wantOK bool
	}{
		{
			name:   "unavailable => ok=false (not folded)",
			pd:     PlatformDetail{Available: false},
			want:   Unknown,
			wantOK: false,
		},
		{
			name:   "json overall DEGRADED wins over exit code",
			pd:     PlatformDetail{Available: true, ExitCode: 0, JSON: json.RawMessage(`{"overall":"DEGRADED"}`)},
			want:   Degraded,
			wantOK: true,
		},
		{
			name:   "json overall HEALTHY",
			pd:     PlatformDetail{Available: true, ExitCode: 1, JSON: json.RawMessage(`{"overall":"HEALTHY"}`)},
			want:   Healthy,
			wantOK: true,
		},
		{
			name:   "no json: exit 1 => Degraded",
			pd:     PlatformDetail{Available: true, ExitCode: 1},
			want:   Degraded,
			wantOK: true,
		},
		{
			name:   "no json: exit 0 => Healthy",
			pd:     PlatformDetail{Available: true, ExitCode: 0},
			want:   Healthy,
			wantOK: true,
		},
		{
			name:   "json overall UNKNOWN",
			pd:     PlatformDetail{Available: true, ExitCode: 0, JSON: json.RawMessage(`{"overall":"UNKNOWN"}`)},
			want:   Unknown,
			wantOK: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := c.pd.Verdict()
			if got != c.want || ok != c.wantOK {
				t.Fatalf("Verdict() = (%s,%v); want (%s,%v)", got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TestRender_UnknownLinesHonest: a system install read without sudo renders
// honest "unknown (re-run with sudo)" lines and exits via Unknown→0, not a
// hard failure or a DOWN.
func TestRender_UnknownLinesHonest(t *testing.T) {
	s := Snapshot{Mode: "system", Found: true, MeshUnknown: true, VersionsUnknown: true}
	res := Assess(s)
	if res.Verdict != Unknown {
		t.Fatalf("verdict = %s; want Unknown", res.Verdict)
	}

	var txt bytes.Buffer
	RenderText(s, res, PlatformDetail{Available: false}, &txt, false)
	out := txt.String()
	if !strings.Contains(out, "unknown (re-run with sudo)") {
		t.Errorf("expected honest sudo hint in output:\n%s", out)
	}
	assertNoLeak(t, "unknown text", out)
}
