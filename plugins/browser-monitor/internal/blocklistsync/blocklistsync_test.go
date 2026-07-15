package blocklistsync_test

import (
	"os"
	"testing"

	"github.com/eliteGoblin/focusd/plugins/browser-monitor/internal/blocklistsync"
	"github.com/eliteGoblin/focusd/plugins/browser-monitor/internal/guard"
)

// TestPythonBlocklistMatchesGoSourceOfTruth is the DRIFT GUARD: the committed
// Python util's generated BLOCKLIST must equal guard.DefaultBlocklist exactly.
// If this fails, someone changed one side without regenerating — run
// `go generate ./...` in plugins/browser-monitor.
func TestPythonBlocklistMatchesGoSourceOfTruth(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	py, err := blocklistsync.RepoPythonPath(cwd)
	if err != nil {
		t.Fatalf("locate python util: %v", err)
	}
	src, err := os.ReadFile(py)
	if err != nil {
		t.Fatal(err)
	}
	got, err := blocklistsync.Extract(src)
	if err != nil {
		t.Fatalf("extract python blocklist: %v", err)
	}
	want := guard.DefaultBlocklist
	if len(got) != len(want) {
		t.Fatalf("python blocklist has %d entries, Go has %d — run `go generate ./...`\npython=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: python=%q Go=%q — run `go generate ./...`", i, got[i], want[i])
		}
	}
}

func TestRenderSpliceExtractRoundTrip(t *testing.T) {
	entries := []string{"a.com", "b.co.uk", "c.io"}
	file := []byte("prefix\n" +
		blocklistsync.BeginMarker + " old\nBLOCKLIST = [\n    \"stale.com\",\n]\n" + blocklistsync.EndMarker + " old\n" +
		"suffix\n")

	spliced, err := blocklistsync.Splice(file, blocklistsync.RenderPython(entries))
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	// Surrounding content is preserved.
	if s := string(spliced); !contains(s, "prefix\n") || !contains(s, "\nsuffix\n") {
		t.Errorf("splice clobbered surrounding content:\n%s", s)
	}
	got, err := blocklistsync.Extract(spliced)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != len(entries) {
		t.Fatalf("round-trip got %v, want %v", got, entries)
	}
	for i := range entries {
		if got[i] != entries[i] {
			t.Errorf("round-trip entry %d = %q, want %q", i, got[i], entries[i])
		}
	}
}

func TestSpliceMissingMarkersErrors(t *testing.T) {
	if _, err := blocklistsync.Splice([]byte("no markers here"), "x"); err == nil {
		t.Error("expected error when markers are absent")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
