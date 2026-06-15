package reconciler

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// launchCall records one launcher invocation for assertions.
type launchCall struct {
	name string
	args []string
}

// fakeReconciler wires a Reconciler with fully faked OS seams: a static
// process table, a recording launcher, and a stat that reports present.
type harness struct {
	r     *Reconciler
	mu    sync.Mutex
	calls []launchCall
}

func newHarness(procs []procView, present bool, launchErr map[string]error) *harness {
	h := &harness{}
	r := New(Options{})
	r.stat = func(string) bool { return present }
	r.list = func() ([]procView, error) { return procs, nil }
	r.launch = func(_ context.Context, name string, args ...string) error {
		h.mu.Lock()
		h.calls = append(h.calls, launchCall{name: name, args: append([]string(nil), args...)})
		h.mu.Unlock()
		if launchErr != nil {
			return launchErr[name]
		}
		return nil
	}
	h.r = r
	return h
}

func appProc() procView   { return procView{PID: 100, Path: DefaultAppProcess} }
func proxyProc() procView { return procView{PID: 200, Path: DefaultProxyProcess} }

// ---------------------------------------------------------------------------
// Acceptance #1 — relaunch-on-kill: down => relaunch with correct cmd/args;
// up => no-op. Table-driven over the four up/down combinations.
// ---------------------------------------------------------------------------

func TestReconcile_RelaunchOnKill(t *testing.T) {
	tests := []struct {
		name           string
		procs          []procView
		wantApp        bool // expected AppRunning
		wantProxy      bool // expected ProxyRunning
		wantRelaunched []string
		wantCalls      []launchCall
	}{
		{
			name:           "both up => no-op",
			procs:          []procView{appProc(), proxyProc()},
			wantApp:        true,
			wantProxy:      true,
			wantRelaunched: nil,
			wantCalls:      nil,
		},
		{
			name:           "app down => relaunch app via open -a",
			procs:          []procView{proxyProc()},
			wantApp:        false,
			wantProxy:      true,
			wantRelaunched: []string{"app"},
			wantCalls: []launchCall{
				{name: "open", args: []string{"-a", DefaultAppPath}},
			},
		},
		{
			name:           "proxy down => relaunch proxy with expected args",
			procs:          []procView{appProc()},
			wantApp:        true,
			wantProxy:      false,
			wantRelaunched: []string{"proxy"},
			wantCalls: []launchCall{
				{name: DefaultProxyProcess, args: []string{"-port", DefaultProxyPort, "-rpcport", DefaultProxyRPCPort}},
			},
		},
		{
			name:           "both down => relaunch both",
			procs:          []procView{{PID: 1, Path: "/usr/bin/Finder"}},
			wantApp:        false,
			wantProxy:      false,
			wantRelaunched: []string{"app", "proxy"},
			wantCalls: []launchCall{
				{name: "open", args: []string{"-a", DefaultAppPath}},
				{name: DefaultProxyProcess, args: []string{"-port", DefaultProxyPort, "-rpcport", DefaultProxyRPCPort}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(tc.procs, true, nil)
			out, err := h.r.Reconcile(context.Background())
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if out.AppRunning != tc.wantApp || out.ProxyRunning != tc.wantProxy {
				t.Errorf("running app=%v proxy=%v, want app=%v proxy=%v",
					out.AppRunning, out.ProxyRunning, tc.wantApp, tc.wantProxy)
			}
			if !equalStrings(out.Relaunched, tc.wantRelaunched) {
				t.Errorf("relaunched=%v, want %v", out.Relaunched, tc.wantRelaunched)
			}
			assertCalls(t, sortedCalls(h.calls), sortedCalls(tc.wantCalls))
			if len(out.Failed) != 0 {
				t.Errorf("unexpected failures: %v", out.Failed)
			}
		})
	}
}

// Idempotence: a second pass once both are up performs no launches.
func TestReconcile_IdempotentWhenAllUp(t *testing.T) {
	h := newHarness([]procView{appProc(), proxyProc()}, true, nil)
	for i := 0; i < 3; i++ {
		out, err := h.r.Reconcile(context.Background())
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if len(out.Relaunched) != 0 {
			t.Fatalf("pass %d relaunched %v, want none", i, out.Relaunched)
		}
	}
	if len(h.calls) != 0 {
		t.Errorf("expected zero launches across idempotent passes, got %v", h.calls)
	}
}

// matchesAny must distinguish Freedom from FreedomProxy (substring guard)
// and honour the basename fallback when only Name is reported.
func TestMatchesAny_PathAndNameFallback(t *testing.T) {
	tests := []struct {
		name   string
		procs  []procView
		target string
		want   bool
	}{
		{"exact path app", []procView{appProc()}, DefaultAppProcess, true},
		{"exact path proxy", []procView{proxyProc()}, DefaultProxyProcess, true},
		{"app path is not proxy", []procView{appProc()}, DefaultProxyProcess, false},
		{"proxy path is not app", []procView{proxyProc()}, DefaultAppProcess, false},
		{"name fallback matches basename", []procView{{PID: 9, Name: "Freedom"}}, DefaultAppProcess, true},
		{"name fallback proxy basename", []procView{{PID: 9, Name: "FreedomProxy"}}, DefaultProxyProcess, true},
		{"name Freedom != proxy target", []procView{{PID: 9, Name: "Freedom"}}, DefaultProxyProcess, false},
		{"unrelated", []procView{{PID: 9, Name: "Finder"}}, DefaultAppProcess, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesAny(tc.procs, tc.target); got != tc.want {
				t.Errorf("matchesAny=%v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Acceptance #2 — well-behaved job: the reconcile returns promptly even if a
// launch hangs; every launch is bounded by a timeout context; enumeration
// failure is a clean error (no panic/hang); Freedom-absent is a benign skip.
// ---------------------------------------------------------------------------

func TestReconcile_LaunchHangDoesNotStall(t *testing.T) {
	r := New(Options{})
	r.stat = func(string) bool { return true }
	r.list = func() ([]procView, error) { return nil, nil } // both down

	// A launcher that blocks until ctx is cancelled — i.e. a hung launch.
	// The bounded context (runLaunch) must cancel it; Reconcile must
	// return promptly rather than hang forever.
	launched := make(chan struct{}, 4)
	r.launch = func(ctx context.Context, _ string, _ ...string) error {
		launched <- struct{}{}
		<-ctx.Done() // block until the timeout/cancel fires
		return ctx.Err()
	}

	// Drive Reconcile under a short caller deadline so the test fails fast
	// if the bound is missing. The launchTimeout (10s) would otherwise be
	// the ceiling; the caller deadline proves the ctx is actually wired in.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan Outcome, 1)
	go func() {
		out, _ := r.Reconcile(ctx)
		done <- out
	}()

	select {
	case out := <-done:
		// Both launches "hung" => both recorded as failures, none relaunched.
		if len(out.Relaunched) != 0 {
			t.Errorf("hung launches should not count as relaunched, got %v", out.Relaunched)
		}
		if len(out.Failed) != 2 {
			t.Errorf("expected 2 launch failures from hung launches, got %v", out.Failed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Reconcile hung on a blocking launch — timeout not enforced")
	}
}

func TestReconcile_EnumerationErrorIsClean(t *testing.T) {
	r := New(Options{})
	r.stat = func(string) bool { return true }
	r.list = func() ([]procView, error) { return nil, errors.New("procfs boom") }
	_, err := r.Reconcile(context.Background())
	if err == nil {
		t.Fatal("expected error when enumeration fails")
	}
}

func TestReconcile_SkipsCleanlyWhenFreedomAbsent(t *testing.T) {
	calls := 0
	r := New(Options{})
	r.stat = func(string) bool { return false } // not installed
	r.list = func() ([]procView, error) { calls++; return nil, nil }
	r.launch = func(context.Context, string, ...string) error { calls++; return nil }

	out, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("absent Freedom must not error: %v", err)
	}
	if !out.Skipped || out.SkipReason == "" {
		t.Errorf("expected benign skip, got %+v", out)
	}
	if calls != 0 {
		t.Errorf("absent Freedom must not scan or launch (calls=%d)", calls)
	}
	// Even on skip, the honest login-item note is still surfaced.
	if out.LoginItemNote == "" {
		t.Error("skip outcome should still carry the login-item note")
	}
}

// A launch failure is recorded, not fatal, and the other target still
// relaunches independently.
func TestReconcile_LaunchFailureRecordedNotFatal(t *testing.T) {
	h := newHarness(
		[]procView{{PID: 1, Path: "/usr/bin/Finder"}}, // both down
		true,
		map[string]error{"open": errors.New("LSOpenURLs EPERM")},
	)
	out, err := h.r.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Failed) != 1 {
		t.Errorf("expected 1 recorded failure, got %v", out.Failed)
	}
	if !equalStrings(out.Relaunched, []string{"proxy"}) {
		t.Errorf("proxy should still relaunch despite app failure, got %v", out.Relaunched)
	}
}

// ---------------------------------------------------------------------------
// Acceptance #3 — login item: each reconcile records the honest best-effort /
// manual-verify note. We never claim a machine-verified re-enable.
// ---------------------------------------------------------------------------

func TestReconcile_LoginItemNoteIsHonest(t *testing.T) {
	h := newHarness([]procView{appProc(), proxyProc()}, true, nil)
	out, err := h.r.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	note := out.LoginItemNote
	for _, must := range []string{"best-effort", "manual-verify", "not scriptable"} {
		if !contains(note, must) {
			t.Errorf("login-item note missing %q; got %q", must, note)
		}
	}
	// Must NOT over-claim success.
	for _, banned := range []string{"enabled successfully", "re-enabled", "verified"} {
		if contains(note, banned) {
			t.Errorf("login-item note over-claims with %q: %q", banned, note)
		}
	}
}

func TestOptionsOverrideDefaults(t *testing.T) {
	r := New(Options{
		AppPath:      "/custom/Free.app",
		AppProcess:   "/custom/Free.app/Contents/MacOS/Free",
		ProxyProcess: "/custom/Free.app/Contents/MacOS/Px",
		ProxyPort:    "1",
		ProxyRPCPort: "2",
	})
	if r.appPath != "/custom/Free.app" || r.proxyPort != "1" || r.proxyRPCPort != "2" {
		t.Errorf("options not applied: %+v", r)
	}
	// Blank fields fall back to defaults.
	r2 := New(Options{ProxyPort: "  "})
	if r2.proxyPort != DefaultProxyPort || r2.appPath != DefaultAppPath {
		t.Errorf("blank options should fall back to defaults: %+v", r2)
	}
}

// New must wire the real OS seams.
func TestNewWiresRealSeams(t *testing.T) {
	r := New(Options{})
	if r.list == nil || r.launch == nil || r.stat == nil {
		t.Fatal("New must wire real list/launch/stat seams")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func equalStrings(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func sortedCalls(c []launchCall) []launchCall {
	out := append([]launchCall(nil), c...)
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func assertCalls(t *testing.T, got, want []launchCall) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("launch calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i].name != want[i].name || !reflect.DeepEqual(got[i].args, want[i].args) {
			t.Errorf("call[%d] = {%s %v}, want {%s %v}",
				i, got[i].name, got[i].args, want[i].name, want[i].args)
		}
	}
}
