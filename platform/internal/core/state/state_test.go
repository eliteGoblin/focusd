package state

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(p string) error { return os.WriteFile(p, []byte("x"), 0o644) }

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrationsApplyAndAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	v, err := db.SchemaVersion()
	if err != nil || v != len(migrations) {
		t.Fatalf("schema version = %d (err %v), want %d", v, err, len(migrations))
	}
	db.Close()

	// Re-open: migrations must not re-run or error.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer db2.Close()
	if v2, _ := db2.SchemaVersion(); v2 != len(migrations) {
		t.Fatalf("schema version after reopen = %d", v2)
	}
}

func TestPluginUpsertPreservesDiscoveredAt(t *testing.T) {
	db := openTest(t)
	p := Plugin{ID: "kill-steam", Name: "Kill Steam", Version: "1.0.0",
		Type: "job", ProtocolVersion: "1", Entrypoint: "./kill-steam",
		Path: "/plugins/kill-steam", SupportedOS: "darwin", SupportedArch: "arm64",
		RequiredPrivilege: "user", RunAs: "current_user", Enabled: true,
		ValidationStatus: ValidationOK}
	if err := db.Plugins.Upsert(p); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := db.Plugins.Get("kill-steam")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	first := got.DiscoveredAt
	if first == "" {
		t.Fatal("discovered_at not set")
	}
	if !got.Enabled {
		t.Error("enabled not persisted")
	}

	time.Sleep(2 * time.Millisecond)
	p.Version = "1.1.0"
	if err := db.Plugins.Upsert(p); err != nil {
		t.Fatalf("re-Upsert: %v", err)
	}
	got2, _ := db.Plugins.Get("kill-steam")
	if got2.DiscoveredAt != first {
		t.Errorf("discovered_at changed on upsert: %s -> %s", first, got2.DiscoveredAt)
	}
	if got2.Version != "1.1.0" {
		t.Errorf("version not updated: %s", got2.Version)
	}
	if got2.LastSeenAt == first {
		t.Error("last_seen_at should advance on re-discovery")
	}

	all, _ := db.Plugins.List()
	if len(all) != 1 {
		t.Fatalf("List len = %d, want 1", len(all))
	}
}

func TestJobRunLifecycle(t *testing.T) {
	db := openTest(t)
	id, err := db.Runs.Start("job1", "kill-steam", "1.0.0", "scheduler")
	if err != nil || id == 0 {
		t.Fatalf("Start: id=%d err=%v", id, err)
	}
	err = db.Runs.Finish(JobRun{ID: id, DurationMS: 42, Status: RunStatusOK,
		ExitCode: 0, Message: "Steam not running", StdoutJSON: `{"status":"ok"}`})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	last, err := db.Runs.LastByStatus("job1", RunStatusOK)
	if err != nil {
		t.Fatalf("LastByStatus: %v", err)
	}
	if last.ID != id || last.DurationMS != 42 || last.EndedAt == "" {
		t.Errorf("unexpected run: %+v", last)
	}

	if _, err := db.Runs.LastByStatus("job1", RunStatusFailed); err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows for absent status, got %v", err)
	}

	if err := db.Runs.RecordSkipped("job1", "kill-steam", "previous run active"); err != nil {
		t.Fatalf("RecordSkipped: %v", err)
	}
	hist, _ := db.Runs.History("job1", 10)
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2", len(hist))
	}
	if hist[0].Status != RunStatusSkipped {
		t.Errorf("newest run status = %s, want skipped", hist[0].Status)
	}
}

func TestJobLockNoOverlap(t *testing.T) {
	db := openTest(t)

	ok, err := db.Locks.TryAcquire("job1", 1, time.Minute)
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	held, _ := db.Locks.Held("job1")
	if !held {
		t.Error("lock should be held")
	}

	// Second acquire while held must fail (no-overlap).
	ok2, err := db.Locks.TryAcquire("job1", 2, time.Minute)
	if err != nil {
		t.Fatalf("second acquire err: %v", err)
	}
	if ok2 {
		t.Error("second acquire should fail while lock held")
	}

	if err := db.Locks.Release("job1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if held, _ := db.Locks.Held("job1"); held {
		t.Error("lock should be free after release")
	}

	// A different job is independent.
	if ok, _ := db.Locks.TryAcquire("job2", 3, time.Minute); !ok {
		t.Error("independent job lock should acquire")
	}
}

func TestJobLockExpiryReclaimed(t *testing.T) {
	db := openTest(t)
	ok, err := db.Locks.TryAcquire("job1", 1, time.Nanosecond)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	time.Sleep(2 * time.Millisecond)
	if held, _ := db.Locks.Held("job1"); held {
		t.Error("expired lock should not be held")
	}
	ok2, err := db.Locks.TryAcquire("job1", 2, time.Minute)
	if err != nil || !ok2 {
		t.Errorf("expired lock should be reclaimable: ok=%v err=%v", ok2, err)
	}
}

func TestJobList(t *testing.T) {
	db := openTest(t)
	for _, id := range []string{"b", "a"} {
		if err := db.Jobs.Upsert(Job{ID: id, PluginID: "p", Schedule: "* * * * *"}); err != nil {
			t.Fatalf("Upsert %s: %v", id, err)
		}
	}
	jobs, err := db.Jobs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != "a" || jobs[1].ID != "b" {
		t.Errorf("List not ordered by id: %+v", jobs)
	}
}

func TestPluginGetNotFound(t *testing.T) {
	db := openTest(t)
	if _, err := db.Plugins.Get("absent"); err != sql.ErrNoRows {
		t.Errorf("Get(absent) err = %v, want ErrNoRows", err)
	}
}

func TestOpenFailsWhenStateDirPathIsAFile(t *testing.T) {
	// A regular file where the state dir should be -> MkdirAll fails.
	f := filepath.Join(t.TempDir(), "blocker")
	if err := writeFile(f); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(f, "state.db")); err == nil {
		t.Error("expected Open to fail when parent path is a file")
	}
}

func TestEvents(t *testing.T) {
	db := openTest(t)
	if err := db.Events.Record(SeverityWarn, "job_skipped", "overlap", `{"job":"j1"}`); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ev, err := db.Events.Recent(10)
	if err != nil || len(ev) != 1 {
		t.Fatalf("Recent: len=%d err=%v", len(ev), err)
	}
	if ev[0].EventType != "job_skipped" || ev[0].Severity != SeverityWarn {
		t.Errorf("unexpected event %+v", ev[0])
	}
}

func TestJobUpsert(t *testing.T) {
	db := openTest(t)
	j := Job{ID: "j1", PluginID: "kill-steam", Enabled: true,
		Schedule: "*/5 * * * *", TimeoutMS: 10000, Retry: 1, AllowOverlap: false}
	if err := db.Jobs.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := db.Jobs.Get("j1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Enabled || got.TimeoutMS != 10000 || got.AllowOverlap {
		t.Errorf("unexpected job %+v", got)
	}
	created := got.CreatedAt
	time.Sleep(2 * time.Millisecond)
	j.Schedule = "0 * * * *"
	_ = db.Jobs.Upsert(j)
	got2, _ := db.Jobs.Get("j1")
	if got2.CreatedAt != created {
		t.Error("created_at must be preserved")
	}
	if got2.Schedule != "0 * * * *" {
		t.Error("schedule not updated")
	}
}
