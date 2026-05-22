package uninstallgate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

func TestStatePathPerMode(t *testing.T) {
	u := StatePath(mode.User, "/Users/alice")
	s := StatePath(mode.System, "/Users/alice")
	if u == s {
		t.Fatal("user and system gate state must not share a path")
	}
	if filepath.Base(u) != stateFile {
		t.Fatalf("unexpected state filename: %s", u)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), stateFile)
	want := State{Step: 2, T1: t0, T2: t0.Add(Step1Wait), LastSeen: t0.Add(Step1Wait)}
	if err := Save(p, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := Load(p, want.LastSeen.Add(time.Hour))
	if got.Step != want.Step || !got.T1.Equal(want.T1) || !got.T2.Equal(want.T2) {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestLoadMissingFileResetsToStart(t *testing.T) {
	if s := Load(filepath.Join(t.TempDir(), "nope"), t0); s != (State{}) {
		t.Fatalf("missing file must reset to zero state, got %+v", s)
	}
}

func TestLoadCorruptJSONResets(t *testing.T) {
	p := filepath.Join(t.TempDir(), stateFile)
	os.WriteFile(p, []byte("{not json"), 0o600)
	if s := Load(p, t0); s != (State{}) {
		t.Fatalf("corrupt file must reset, got %+v", s)
	}
}

func TestLoadTamperedStateResets(t *testing.T) {
	p := filepath.Join(t.TempDir(), stateFile)
	if err := Save(p, State{Step: 1, T1: t0, LastSeen: t0}); err != nil {
		t.Fatal(err)
	}
	// Hand-edit the state payload but keep the (now stale) MAC.
	raw, _ := os.ReadFile(p)
	var env envelope
	json.Unmarshal(raw, &env)
	env.State = json.RawMessage(`{"step":3,"last_seen":"2026-05-18T09:00:00Z"}`)
	bad, _ := json.Marshal(env)
	os.WriteFile(p, bad, 0o600)

	if s := Load(p, t0.Add(time.Hour)); s != (State{}) {
		t.Fatalf("HMAC mismatch must reset to step 1, got %+v", s)
	}
}

func TestLoadClockRollbackResets(t *testing.T) {
	p := filepath.Join(t.TempDir(), stateFile)
	saved := State{Step: 2, T1: t0, T2: t0.Add(Step1Wait), LastSeen: t0.Add(Step1Wait)}
	if err := Save(p, saved); err != nil {
		t.Fatal(err)
	}
	// "now" is before LastSeen ⇒ the clock moved backwards ⇒ reset.
	if s := Load(p, t0); s != (State{}) {
		t.Fatalf("clock rollback must reset, got %+v", s)
	}
}

func TestClear(t *testing.T) {
	p := filepath.Join(t.TempDir(), stateFile)
	Save(p, State{Step: 1, LastSeen: t0})
	if err := Clear(p); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("state file must be gone after Clear")
	}
	if err := Clear(p); err != nil {
		t.Fatalf("Clear on missing file must be nil, got %v", err)
	}
}

func TestLoadValidMACButBrokenStateResets(t *testing.T) {
	// Envelope whose MAC correctly authenticates a payload that is not a
	// valid State (e.g. a future/garbled schema). Must reset, not crash.
	p := filepath.Join(t.TempDir(), stateFile)
	payload := []byte(`["not", "an", "object"]`)
	env := envelope{State: payload, MAC: mac(payload)}
	out, _ := json.Marshal(env)
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
	if s := Load(p, t0); s != (State{}) {
		t.Fatalf("authenticated-but-unparseable state must reset, got %+v", s)
	}
}

func TestSaveErrorWhenParentNotADir(t *testing.T) {
	// A regular file stands where a directory would need to be → the
	// MkdirAll inside Save must surface an error (not panic/silently).
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(filepath.Join(f, "sub", stateFile), State{Step: 1}); err == nil {
		t.Fatal("expected an error saving under a non-directory")
	}
}

func TestSaveThenTamperDetectionIsStable(t *testing.T) {
	// A faithfully saved state loads back; flipping one MAC char resets.
	p := filepath.Join(t.TempDir(), stateFile)
	Save(p, State{Step: 1, T1: t0, LastSeen: t0})
	raw, _ := os.ReadFile(p)
	var env envelope
	json.Unmarshal(raw, &env)
	if len(env.MAC) == 0 {
		t.Fatal("expected a MAC")
	}
	env.MAC = "0" + env.MAC[1:]
	bad, _ := json.Marshal(env)
	os.WriteFile(p, bad, 0o600)
	if s := Load(p, t0); s != (State{}) {
		t.Fatalf("flipped MAC must reset, got %+v", s)
	}
}
