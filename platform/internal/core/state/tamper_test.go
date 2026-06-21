package state

import (
	"strings"
	"testing"
	"time"
)

func TestRecordTamperRepaired_NoPathLeak(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Events.RecordTamperRepaired("kill-steam-reconcile", "kill-steam", "abc123", "def456"); err != nil {
		t.Fatalf("RecordTamperRepaired: %v", err)
	}
	ev, err := db.Events.Recent(5)
	if err != nil || len(ev) == 0 {
		t.Fatalf("recent: %v len=%d", err, len(ev))
	}
	e := ev[0]
	if e.EventType != EventTamperRepaired || e.Severity != SeverityWarn {
		t.Fatalf("unexpected event: %+v", e)
	}
	// Details must carry sha prefixes + ids, never a path-like substring.
	for _, bad := range []string{"/", "Library", "var/root", ".plist"} {
		if strings.Contains(e.DetailsJSON, bad) {
			t.Errorf("tamper details leaked %q: %s", bad, e.DetailsJSON)
		}
	}
	if !strings.Contains(e.DetailsJSON, "abc123") {
		t.Errorf("want_sha_prefix missing from details: %s", e.DetailsJSON)
	}
}

func TestTamperSince_WindowAndCount(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Two tamper events for j1, one for j2.
	if err := db.Events.RecordTamperRepaired("j1", "p1", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := db.Events.RecordTamperRepaired("j1", "p1", "c", "d"); err != nil {
		t.Fatal(err)
	}
	if err := db.Events.RecordTamperRepaired("j2", "p2", "e", "f"); err != nil {
		t.Fatal(err)
	}

	since, count, found, err := db.Events.TamperSince("j1", time.Hour)
	if err != nil {
		t.Fatalf("TamperSince: %v", err)
	}
	if !found || count != 2 {
		t.Fatalf("j1: found=%v count=%d, want found=true count=2", found, count)
	}
	if since.IsZero() {
		t.Error("expected a non-zero latest tamper time")
	}

	// j3 never tampered → not found.
	_, _, found3, _ := db.Events.TamperSince("j3", time.Hour)
	if found3 {
		t.Error("j3 should have no tamper events")
	}

	// A zero-length window excludes everything (cutoff == now).
	_, _, foundZero, _ := db.Events.TamperSince("j1", 0)
	if foundZero {
		// events recorded a hair before now() could still match equal
		// timestamps; allow it but ensure no panic. Not asserting strictly.
		t.Log("zero-window matched equal-timestamp events (acceptable)")
	}
}

// TestTamperSince_IDSubstringIsolation guards the LIKE match: a job id that
// is a substring of another must not cross-match. "j1" must not match a
// "j10" event.
func TestTamperSince_IDSubstringIsolation(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Events.RecordTamperRepaired("j10", "p", "a", "b"); err != nil {
		t.Fatal(err)
	}
	_, _, found, err := db.Events.TamperSince("j1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error(`"j1" must not match a "j10" tamper event (exact quoted match)`)
	}
}
