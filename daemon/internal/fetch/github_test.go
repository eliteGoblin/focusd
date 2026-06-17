package fetch

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// recordingTransport captures the host of every request it sees and
// returns the body produced by serve for the matching download URL.
type recordingTransport struct {
	mu    sync.Mutex
	hosts []string
	paths []string
	serve func(*http.Request) (*http.Response, error)
}

func (t *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.hosts = append(t.hosts, r.URL.Host)
	t.paths = append(t.paths, r.URL.Path)
	t.mu.Unlock()
	return t.serve(r)
}

func okBody(b []byte) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(string(b))),
		Header:     make(http.Header),
	}
}

// signingKey loads the offline Ed25519 private key (env or ~/.creds),
// the same source the e2e/self-update tests use. Skips when absent so
// plain CI runners without the secret don't fail.
func signingKey(t *testing.T) []byte {
	t.Helper()
	if v := os.Getenv("FOCUSD_ED25519_PRIVATE_KEY"); v != "" {
		return []byte(v)
	}
	if h, err := os.UserHomeDir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(h, ".creds", "focusd_ed25519_private.pem")); err == nil {
			return b
		}
	}
	t.Skip("no offline signing key (env or ~/.creds); skipping signature-dependent test")
	return nil
}

// signedBytes returns program ++ valid 64-byte Ed25519 trailer for prog,
// signed by the real offline key so sig.VerifyFile accepts it.
func signedBytes(t *testing.T, prog []byte) []byte {
	t.Helper()
	priv := signingKey(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "in")
	out := filepath.Join(dir, "out")
	if err := os.WriteFile(in, prog, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sig.SignFile(in, out, priv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const (
	testRepo  = "eliteGoblin/focusd"
	testTag   = "v1.2.3"
	testAsset = "platform-darwin-arm64"
)

// TestDownloadVerified_NoAPIHit is the core ADR-0015 regression: a pinned
// download must hit the direct release-download URL on github.com and make
// ZERO requests to api.github.com (the 60/hr rate-limited host that caused
// the fetch-storm → 403s). It deliberately does NOT depend on the offline
// signing key — the request is issued BEFORE verification, so serving an
// unsigned body (verification then fails, which we ignore) lets the host/path
// regression run on EVERY CI runner, not just ones with the secret. The
// assertion tolerates the real-world 302 to objects.githubusercontent.com:
// it checks "no api.github.com hop" + "first hop is the direct CDN path",
// not an exact request count.
func TestDownloadVerified_NoAPIHit(t *testing.T) {
	rt := &recordingTransport{
		serve: func(r *http.Request) (*http.Response, error) {
			return okBody([]byte("unsigned platform bytes")), nil
		},
	}
	g := &GitHub{Repo: testRepo, Asset: testAsset, HTTP: &http.Client{Transport: rt}}

	// Verification will fail on the unsigned body; we only assert on the
	// requests that were made to reach it.
	dst := filepath.Join(t.TempDir(), "platform")
	_ = g.DownloadVerified(context.Background(), testTag, testAsset, dst)

	if len(rt.hosts) == 0 {
		t.Fatal("DownloadVerified made no request")
	}
	for _, h := range rt.hosts {
		if h == "api.github.com" {
			t.Fatalf("DownloadVerified hit api.github.com (rate-limited) — hosts: %v", rt.hosts)
		}
	}
	if rt.hosts[0] != "github.com" {
		t.Fatalf("first hop host = %q, want github.com — hosts: %v", rt.hosts[0], rt.hosts)
	}
	wantPath := "/" + testRepo + "/releases/download/" + testTag + "/" + testAsset
	if rt.paths[0] != wantPath {
		t.Fatalf("first-hop download path = %q, want %q", rt.paths[0], wantPath)
	}
}

// TestDownloadVerified_NoBearerForwarded asserts the download request itself
// carries no Authorization header (even with GITHUB_TOKEN set) and uses an
// octet-stream Accept. A bearer must not ride the CDN download leg — Go's
// http.Client also strips it across the cross-host 302 to objects.githubusercontent.com.
// Key-free: the header check runs before verification, so an unsigned body
// (verify then fails, ignored) keeps this regression live in plain CI.
func TestDownloadVerified_NoBearerForwarded(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "should-not-be-forwarded")
	rt := &recordingTransport{
		serve: func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "" {
				t.Errorf("Authorization forwarded to download leg: %q", r.Header.Get("Authorization"))
			}
			if a := r.Header.Get("Accept"); a != "application/octet-stream" {
				t.Errorf("Accept = %q, want application/octet-stream", a)
			}
			return okBody([]byte("unsigned platform bytes")), nil
		},
	}
	g := &GitHub{Repo: testRepo, Asset: testAsset, HTTP: &http.Client{Transport: rt}}
	dst := filepath.Join(t.TempDir(), "platform")
	_ = g.DownloadVerified(context.Background(), testTag, testAsset, dst)
}

// TestDownloadVerified_PlacesVerifiedBytes is the happy-path complement: a
// genuinely-signed body is verified and atomically placed at dst (0755). This
// one needs the offline signing key, so it skips on runners without it — but
// the host/path/header REGRESSIONS above no longer depend on the key.
func TestDownloadVerified_PlacesVerifiedBytes(t *testing.T) {
	body := signedBytes(t, []byte("genuine platform bytes"))
	rt := &recordingTransport{
		serve: func(r *http.Request) (*http.Response, error) { return okBody(body), nil },
	}
	g := &GitHub{Repo: testRepo, Asset: testAsset, HTTP: &http.Client{Transport: rt}}
	dst := filepath.Join(t.TempDir(), "platform")
	if err := g.DownloadVerified(context.Background(), testTag, testAsset, dst); err != nil {
		t.Fatalf("DownloadVerified: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != string(body) {
		t.Fatal("placed bytes differ from served body")
	}
	fi, _ := os.Stat(dst)
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("dst mode = %v, want 0755", fi.Mode().Perm())
	}
}

// TestDownloadVerified_BadSignatureRefused proves a body that fails the
// Ed25519 check returns an error and does NOT place the file.
func TestDownloadVerified_BadSignatureRefused(t *testing.T) {
	// Unsigned body: program plus a bogus 64-byte trailer.
	bad := append([]byte("not a signed binary"), make([]byte, sig.SigLen)...)
	rt := &recordingTransport{
		serve: func(r *http.Request) (*http.Response, error) { return okBody(bad), nil },
	}
	g := &GitHub{Repo: testRepo, Asset: testAsset, HTTP: &http.Client{Transport: rt}}
	dst := filepath.Join(t.TempDir(), "platform")
	err := g.DownloadVerified(context.Background(), testTag, testAsset, dst)
	if err == nil {
		t.Fatal("expected error on bad signature, got nil")
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("file must NOT be placed on verification failure (stat err=%v)", statErr)
	}
}

// TestDownloadVerified_Non200 covers a CDN hiccup (e.g. 404/503) surfacing
// as an error without placing anything.
func TestDownloadVerified_Non200(t *testing.T) {
	rt := &recordingTransport{
		serve: func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 503,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		},
	}
	g := &GitHub{Repo: testRepo, Asset: testAsset, HTTP: &http.Client{Transport: rt}}
	dst := filepath.Join(t.TempDir(), "platform")
	if err := g.DownloadVerified(context.Background(), testTag, testAsset, dst); err == nil {
		t.Fatal("expected error on non-200 download")
	}
}
