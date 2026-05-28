package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliteGoblin/focusd/plugins/network-block/internal/dns"
)

// --- helpers ---

func writeCfg(t *testing.T, dir string, body string) string {
	t.Helper()
	p := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// captureStdout redirects os.Stdout, runs fn, and returns whatever it
// printed. Necessary because emit() prints the success-path JSON and
// the E2E test wants to assert its shape.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wr
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(rd)
		done <- string(b)
	}()

	fn()
	_ = wr.Close()
	return <-done
}

// fakePf implements reconciler.PfTable in-memory so the E2E test can
// observe what the plugin would have called.
type fakePf struct {
	current  []string
	addCalls []string
	delCalls []string
}

func (f *fakePf) Show(_ context.Context) ([]string, error) {
	return append([]string{}, f.current...), nil
}
func (f *fakePf) Add(_ context.Context, ip string) error {
	f.addCalls = append(f.addCalls, ip)
	return nil
}
func (f *fakePf) Delete(_ context.Context, ip string) error {
	f.delCalls = append(f.delCalls, ip)
	return nil
}

// --- unit-level CLI plumbing ---

func TestRun_UsageAndVersion(t *testing.T) {
	if code := run([]string{"version"}); code != 0 {
		t.Errorf("version exit = %d, want 0", code)
	}
	if code := run([]string{}); code != 2 {
		t.Errorf("no args exit = %d, want 2", code)
	}
	if code := run([]string{"nonsense"}); code != 2 {
		t.Errorf("bad subcommand exit = %d, want 2", code)
	}
}

func TestRun_MissingConfigFlag(t *testing.T) {
	if code := run([]string{"run"}); code != 2 {
		t.Errorf("missing --config exit = %d, want 2", code)
	}
}

func TestRun_MissingConfigFile(t *testing.T) {
	code := run([]string{"run", "--config", "/nonexistent/path/cfg.json"})
	if code != 2 {
		t.Errorf("missing file exit = %d, want 2", code)
	}
}

// --- config validation ---

func TestLoadConfig_RejectsHTTPResolver(t *testing.T) {
	dir := t.TempDir()
	p := writeCfg(t, dir, `{"config":{"anchor":"a","table":"t","domains":["x"],"resolver":"http://insecure/"}}`)
	_, err := loadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "https://") {
		t.Errorf("HTTP resolver should be rejected, got err=%v", err)
	}
}

func TestLoadConfig_MissingFields(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"missing anchor":   `{"config":{"table":"t","domains":["x"],"resolver":"https://r/"}}`,
		"missing table":    `{"config":{"anchor":"a","domains":["x"],"resolver":"https://r/"}}`,
		"missing domains":  `{"config":{"anchor":"a","table":"t","resolver":"https://r/"}}`,
		"empty domains":    `{"config":{"anchor":"a","table":"t","domains":[],"resolver":"https://r/"}}`,
		"missing resolver": `{"config":{"anchor":"a","table":"t","domains":["x"]}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeCfg(t, dir, body)
			if _, err := loadConfig(p); err == nil {
				t.Errorf("%s should be rejected", name)
			}
		})
	}
}

func TestLoadConfig_HappyPath(t *testing.T) {
	dir := t.TempDir()
	p := writeCfg(t, dir, `{"job_id":"j","plugin_id":"network-block","config":{
		"anchor":"focusd-block-steam",
		"table":"steam_ips",
		"domains":["a.com","b.com"],
		"resolver":"https://cloudflare-dns.com/dns-query"
	}}`)
	cfg, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Anchor != "focusd-block-steam" ||
		cfg.Table != "steam_ips" ||
		len(cfg.Domains) != 2 ||
		cfg.Resolver != "https://cloudflare-dns.com/dns-query" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadConfig_BadDomainsShape(t *testing.T) {
	dir := t.TempDir()
	p := writeCfg(t, dir, `{"config":{"anchor":"a","table":"t","domains":[123],"resolver":"https://r/"}}`)
	if _, err := loadConfig(p); err == nil {
		t.Error("non-string domain should be rejected")
	}
}

func TestLoadConfig_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	p := writeCfg(t, dir, `{not json`)
	if _, err := loadConfig(p); err == nil {
		t.Error("malformed JSON should be rejected")
	}
}

// --- runWithDeps failure paths ---

// fakeBrokenPf returns errors from Show so we exercise the
// reconcile-error -> exit 1 branch of runWithDeps.
type fakeBrokenPf struct{ fakePf }

func (f *fakeBrokenPf) Show(_ context.Context) ([]string, error) {
	return nil, errIO
}

var errIO = &errString{"pfctl not permitted"}

type errString struct{ s string }

func (e *errString) Error() string { return e.s }

func TestRunWithDeps_ReconcileFailure_ReturnsExit1(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Status":0,"Answer":[{"name":"x","type":1,"TTL":60,"data":"1.1.1.1"}]}`))
	}))
	defer server.Close()
	cfg := pluginConfig{
		Anchor: "a", Table: "t",
		Domains:  []string{"a.com"},
		Resolver: server.URL,
	}
	resolver := dns.NewResolverWithClient(server.URL, server.Client())
	pf := &fakeBrokenPf{}

	code := 0
	stdout := captureStdout(t, func() {
		code = runWithDeps(cfg, resolver, pf)
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (controlled failure)", code)
	}
	var r result
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &r); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, stdout)
	}
	if r.Status != "failed" {
		t.Errorf("status = %q, want failed", r.Status)
	}
}

func TestRunWithDeps_BadResolverURL_ReturnsExit1(t *testing.T) {
	// When resolver is nil, runWithDeps constructs the real one. An
	// http:// URL here forces NewResolver to reject and we get exit 1.
	cfg := pluginConfig{
		Anchor: "a", Table: "t",
		Domains:  []string{"a.com"},
		Resolver: "http://not-https/", // bypassed loadConfig in this test
	}
	code := 0
	_ = captureStdout(t, func() {
		code = runWithDeps(cfg, nil, &fakePf{})
	})
	if code != 1 {
		t.Errorf("exit = %d, want 1 (resolver rejection)", code)
	}
}

// --- E2E: fake DoH + fake pfctl, real entry path ---

func TestE2E_FakeDoH_FakePfctl(t *testing.T) {
	// Fake DoH responds with two A records for any name.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		_, _ = w.Write([]byte(`{"Status":0,"Answer":[
			{"name":"` + name + `","type":1,"TTL":60,"data":"1.1.1.1"},
			{"name":"` + name + `","type":1,"TTL":60,"data":"2.2.2.2"}
		]}`))
	}))
	defer server.Close()

	cfg := pluginConfig{
		Anchor:   "focusd-block-steam",
		Table:    "steam_ips",
		Domains:  []string{"a.com"},
		Resolver: server.URL, // http:// — bypassed via NewResolverWithClient seam
	}
	// Inject the test resolver explicitly so the http:// URL doesn't
	// get rejected by NewResolver. This is the same seam dns_test uses.
	resolver := dns.NewResolverWithClient(server.URL, server.Client())
	pf := &fakePf{current: []string{"9.9.9.9"}}

	var stdout string
	code := 0
	stdout = captureStdout(t, func() {
		code = runWithDeps(cfg, resolver, pf)
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stdout=%s", code, stdout)
	}

	// Parse the stdout JSON envelope.
	var r result
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &r); err != nil {
		t.Fatalf("stdout was not valid JSON: %v\n%s", err, stdout)
	}
	if r.Status != "ok" {
		t.Errorf("status = %q, want ok. raw=%s", r.Status, stdout)
	}
	if !strings.Contains(r.Message, "added=2") || !strings.Contains(r.Message, "removed=1") {
		t.Errorf("message = %q, want counts of added=2 removed=1", r.Message)
	}
	// Verify the fake pf saw the right calls.
	want := []string{"1.1.1.1", "2.2.2.2"}
	if len(pf.addCalls) != 2 || pf.addCalls[0] != want[0] || pf.addCalls[1] != want[1] {
		t.Errorf("addCalls = %v, want %v", pf.addCalls, want)
	}
	if len(pf.delCalls) != 1 || pf.delCalls[0] != "9.9.9.9" {
		t.Errorf("delCalls = %v, want [9.9.9.9]", pf.delCalls)
	}
}
