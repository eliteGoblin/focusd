package platformsvc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStartCapturesEngineLogToFile is the observability guard: the engine's
// stdout AND stderr must be captured to <workdir>/platform.log. Previously the
// child's stdio was left nil → /dev/null, silently discarding every engine and
// plugin log line (and hiding real failures). A fake engine writes to both
// streams; both must show up in the log file.
func TestStartCapturesEngineLogToFile(t *testing.T) {
	wd := t.TempDir()
	script := filepath.Join(wd, "fake-engine")
	body := "#!/bin/sh\necho ENGINE_STDOUT_LINE\necho ENGINE_STDERR_LINE >&2\nsleep 0.3\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	p := New(wd)
	if err := p.Start(script, "v1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-p.exitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("engine did not exit in time")
	}

	b, err := os.ReadFile(filepath.Join(wd, PlatformLogName))
	if err != nil {
		t.Fatalf("read %s: %v", PlatformLogName, err)
	}
	got := string(b)
	if !strings.Contains(got, "ENGINE_STDOUT_LINE") {
		t.Errorf("engine stdout not captured to %s; got: %q", PlatformLogName, got)
	}
	if !strings.Contains(got, "ENGINE_STDERR_LINE") {
		t.Errorf("engine stderr not captured to %s; got: %q", PlatformLogName, got)
	}
}

// TestStartAppendsAcrossRestarts confirms a restart appends (doesn't truncate)
// — log history across engine restarts must be preserved.
func TestStartAppendsAcrossRestarts(t *testing.T) {
	wd := t.TempDir()
	script := filepath.Join(wd, "fake-engine")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho RUN_MARKER\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := New(wd)
	for i := 0; i < 2; i++ {
		if err := p.Start(script, "v1"); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		<-p.exitCh
	}
	b, _ := os.ReadFile(filepath.Join(wd, PlatformLogName))
	if n := strings.Count(string(b), "RUN_MARKER"); n != 2 {
		t.Errorf("expected 2 appended markers across restarts, got %d", n)
	}
}
