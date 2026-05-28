package dns

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewResolver_RejectsNonHTTPS(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"plain http", "http://example.com/dns-query"},
		{"empty", ""},
		{"no scheme", "cloudflare-dns.com/dns-query"},
		{"file scheme", "file:///etc/passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewResolver(tc.url); err == nil {
				t.Errorf("NewResolver(%q) returned nil error, want rejection", tc.url)
			}
		})
	}
}

func TestNewResolver_AcceptsHTTPS(t *testing.T) {
	r, err := NewResolver("https://cloudflare-dns.com/dns-query")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r == nil || r.client == nil {
		t.Fatal("resolver or client is nil")
	}
}

// fakeDoH returns an httptest server that responds with the given body
// and status for any request. Per-test handler can also inspect the
// incoming query string.
func fakeDoH(t *testing.T, status int, body string, inspect func(*http.Request)) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inspect != nil {
			inspect(r)
		}
		w.Header().Set("Content-Type", "application/dns-json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestResolveA_SingleAnswer(t *testing.T) {
	body := `{"Status":0,"Answer":[{"name":"example.com","type":1,"TTL":300,"data":"93.184.216.34"}]}`
	var gotName, gotType string
	s := fakeDoH(t, 200, body, func(r *http.Request) {
		gotName = r.URL.Query().Get("name")
		gotType = r.URL.Query().Get("type")
	})

	r := NewResolverWithClient(s.URL, s.Client())
	ips, err := r.ResolveA(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("ResolveA: %v", err)
	}
	if len(ips) != 1 || ips[0] != "93.184.216.34" {
		t.Errorf("got %v, want [93.184.216.34]", ips)
	}
	if gotName != "example.com" || gotType != "A" {
		t.Errorf("query name=%q type=%q, want example.com / A", gotName, gotType)
	}
}

func TestResolveA_MultipleA(t *testing.T) {
	body := `{"Status":0,"Answer":[
		{"name":"x","type":1,"TTL":60,"data":"1.1.1.1"},
		{"name":"x","type":1,"TTL":60,"data":"2.2.2.2"},
		{"name":"x","type":1,"TTL":60,"data":"3.3.3.3"}
	]}`
	s := fakeDoH(t, 200, body, nil)
	r := NewResolverWithClient(s.URL, s.Client())

	ips, err := r.ResolveA(context.Background(), "x")
	if err != nil {
		t.Fatalf("ResolveA: %v", err)
	}
	want := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	if !equalStrings(ips, want) {
		t.Errorf("got %v, want %v", ips, want)
	}
}

func TestResolveA_AAAAIgnored(t *testing.T) {
	body := `{"Status":0,"Answer":[
		{"name":"x","type":1,"TTL":60,"data":"1.1.1.1"},
		{"name":"x","type":28,"TTL":60,"data":"2606:4700::1111"},
		{"name":"x","type":5,"TTL":60,"data":"cname.target."}
	]}`
	s := fakeDoH(t, 200, body, nil)
	r := NewResolverWithClient(s.URL, s.Client())

	ips, err := r.ResolveA(context.Background(), "x")
	if err != nil {
		t.Fatalf("ResolveA: %v", err)
	}
	if len(ips) != 1 || ips[0] != "1.1.1.1" {
		t.Errorf("got %v, want only [1.1.1.1] (AAAA + CNAME dropped)", ips)
	}
}

func TestResolveA_NXDOMAIN_EmptyResult(t *testing.T) {
	// Status=3 is NXDOMAIN; should be a clean empty result, not error.
	body := `{"Status":3,"Answer":[]}`
	s := fakeDoH(t, 200, body, nil)
	r := NewResolverWithClient(s.URL, s.Client())

	ips, err := r.ResolveA(context.Background(), "nope.invalid")
	if err != nil {
		t.Fatalf("NXDOMAIN should not error: %v", err)
	}
	if len(ips) != 0 {
		t.Errorf("want empty, got %v", ips)
	}
}

func TestResolveA_ResolverErrorStatus(t *testing.T) {
	// Status=2 is SERVFAIL; that's a real failure.
	body := `{"Status":2,"Answer":[]}`
	s := fakeDoH(t, 200, body, nil)
	r := NewResolverWithClient(s.URL, s.Client())

	if _, err := r.ResolveA(context.Background(), "x"); err == nil {
		t.Fatal("SERVFAIL should return an error")
	}
}

func TestResolveA_HTTPErrorStatus(t *testing.T) {
	s := fakeDoH(t, 502, `{"error":"bad gateway"}`, nil)
	r := NewResolverWithClient(s.URL, s.Client())

	_, err := r.ResolveA(context.Background(), "x")
	if err == nil {
		t.Fatal("HTTP 502 should return an error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error %q should mention 502", err)
	}
}

func TestResolveA_MalformedJSON(t *testing.T) {
	s := fakeDoH(t, 200, `{not json`, nil)
	r := NewResolverWithClient(s.URL, s.Client())

	if _, err := r.ResolveA(context.Background(), "x"); err == nil {
		t.Fatal("malformed JSON should return an error")
	}
}

func TestResolveA_MalformedIPDataSkipped(t *testing.T) {
	// Data field that isn't a valid IP should be skipped, not crash.
	body := `{"Status":0,"Answer":[
		{"name":"x","type":1,"TTL":60,"data":"not-an-ip"},
		{"name":"x","type":1,"TTL":60,"data":"1.2.3.4"}
	]}`
	s := fakeDoH(t, 200, body, nil)
	r := NewResolverWithClient(s.URL, s.Client())

	ips, err := r.ResolveA(context.Background(), "x")
	if err != nil {
		t.Fatalf("ResolveA: %v", err)
	}
	if len(ips) != 1 || ips[0] != "1.2.3.4" {
		t.Errorf("got %v, want only [1.2.3.4]", ips)
	}
}

func TestResolveA_EmptyName(t *testing.T) {
	r, err := NewResolver("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveA(context.Background(), ""); err == nil {
		t.Error("empty name should return error")
	}
}

func TestResolveA_NetworkError(t *testing.T) {
	s := fakeDoH(t, 200, `{}`, nil)
	s.Close() // close immediately so the next Do fails
	r := NewResolverWithClient(s.URL, s.Client())

	if _, err := r.ResolveA(context.Background(), "x"); err == nil {
		t.Error("expected network error after server closed")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
