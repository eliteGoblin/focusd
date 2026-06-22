package runner

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/state"
	"github.com/eliteGoblin/focusd/platform/internal/testutil"
)

// capturedLog builds a runner whose action log is captured into a buffer,
// so a test can assert on the exact structured lines the runner emits
// (FEATURE 16 — whitebox action logging).
func capturedLog(t *testing.T) (*Runner, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	r := newRunner(t).WithLogger(slog.New(h))
	return r, &buf
}

// linesAtLevel returns the captured log lines whose level=<LEVEL> field is set.
func linesAtLevel(buf *bytes.Buffer, level string) []string {
	var out []string
	for _, ln := range strings.Split(buf.String(), "\n") {
		if strings.Contains(ln, "level="+level) {
			out = append(out, ln)
		}
	}
	return out
}

// TestLogTamperRepairedWarns pins AC-3: an integrity restore emits a WARN line
// naming the event, the plugin id, and the sha PREFIXES (and nothing else
// path-like).
func TestLogTamperRepairedWarns(t *testing.T) {
	r, buf := capturedLog(t)
	fv := &fakeVerifier{restored: true, wantPrefix: "aabbccddeeff", gotPrefix: "112233445566"}
	r.WithVerifier(fv)

	p := testutil.ScriptPlugin(t, "ok-plugin", `echo '{"status":"ok"}'
exit 0`)
	if _, err := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	warns := linesAtLevel(buf, "WARN")
	if len(warns) != 1 {
		t.Fatalf("want exactly 1 WARN line, got %d:\n%s", len(warns), buf.String())
	}
	w := warns[0]
	for _, want := range []string{
		`msg="plugin tamper repaired"`,
		"plugin=ok-plugin",
		"want_sha=aabbccddeeff",
		"got_sha=112233445566",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("WARN line missing %q:\n%s", want, w)
		}
	}
	if errs := linesAtLevel(buf, "ERROR"); len(errs) != 0 {
		t.Errorf("restore must not log ERROR, got:\n%v", errs)
	}
}

// TestLogIntegrityCheckFailedErrors pins AC: a verify error emits an ERROR
// line naming the event + plugin id, and logs the error CLASS only (never the
// raw error string, which could embed a disguised path).
func TestLogIntegrityCheckFailedErrors(t *testing.T) {
	r, buf := capturedLog(t)
	// An error string containing a path-like token: it MUST NOT appear in the
	// log (we log err_type, not err).
	fv := &fakeVerifier{err: errors.New("open /private/var/folders/secret/x: no such file")}
	r.WithVerifier(fv)

	p := testutil.ScriptPlugin(t, "ok-plugin", `echo '{"status":"ok"}'
exit 0`)
	out, err := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Status != state.RunStatusError {
		t.Fatalf("verify error must yield error status, got %+v", out)
	}

	errs := linesAtLevel(buf, "ERROR")
	if len(errs) != 1 {
		t.Fatalf("want exactly 1 ERROR line, got %d:\n%s", len(errs), buf.String())
	}
	e := errs[0]
	if !strings.Contains(e, `msg="plugin integrity check failed"`) {
		t.Errorf("ERROR line missing event msg:\n%s", e)
	}
	if !strings.Contains(e, "plugin=ok-plugin") {
		t.Errorf("ERROR line missing plugin id:\n%s", e)
	}
	// Redaction: the raw error string (and its path) must NOT leak.
	if strings.Contains(buf.String(), "/private/var/folders") {
		t.Errorf("redaction failure: raw error path leaked into log:\n%s", buf.String())
	}
}

// TestLogCleanRunQuiet pins AC-2: a clean run (no verifier, healthy plugin)
// produces NO WARN and NO ERROR lines from the runner. The per-run INFO
// "job finished" line is emitted by the scheduler, not the runner, so the
// runner itself stays silent on the happy path.
func TestLogCleanRunQuiet(t *testing.T) {
	r, buf := capturedLog(t)
	p := testutil.ScriptPlugin(t, "ok-plugin", `echo '{"status":"ok"}'
exit 0`)
	if _, err := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if warns := linesAtLevel(buf, "WARN"); len(warns) != 0 {
		t.Errorf("clean run must not log WARN, got:\n%v", warns)
	}
	if errs := linesAtLevel(buf, "ERROR"); len(errs) != 0 {
		t.Errorf("clean run must not log ERROR, got:\n%v", errs)
	}
}

// TestLogRedactionNoPathInWarn pins the redaction contract for the tamper
// WARN: the only fields are the plugin id and sha prefixes — there is NO
// '/'-bearing path token anywhere in the line.
func TestLogRedactionNoPathInWarn(t *testing.T) {
	r, buf := capturedLog(t)
	fv := &fakeVerifier{restored: true, wantPrefix: "deadbeef00", gotPrefix: "cafebabe11"}
	r.WithVerifier(fv)

	p := testutil.ScriptPlugin(t, "ok-plugin", `echo '{"status":"ok"}'
exit 0`)
	if _, err := r.Run(context.Background(), Job{ID: "j1"}, p, "scheduler"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	warns := linesAtLevel(buf, "WARN")
	if len(warns) != 1 {
		t.Fatalf("want 1 WARN line, got %d:\n%s", len(warns), buf.String())
	}
	// Strip the slog "time=" field (an RFC3339 timestamp has no '/'); then no
	// remaining token in the WARN line may contain a path separator.
	for _, tok := range strings.Fields(warns[0]) {
		if strings.HasPrefix(tok, "time=") {
			continue
		}
		if strings.ContainsRune(tok, '/') {
			t.Errorf("WARN line leaked a path-like token %q:\n%s", tok, warns[0])
		}
	}
}
