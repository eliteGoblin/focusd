// Package dns implements a minimal Cloudflare-style DNS-over-HTTPS
// (application/dns-json) client used by the network-block plugin to
// resolve domain names to IPv4 addresses without going through the
// system resolver.
//
// We bypass the OS resolver on purpose: the dns-block plugin sibling
// pins steam-style domains to 0.0.0.0 in /etc/hosts, so the system
// resolver would return junk. DoH gives us the truth straight from a
// public resolver (default Cloudflare 1.1.1.1).
//
// Scope intentionally small:
//   - A records only (IPv4). AAAA results are ignored because the pf
//     table on darwin holds v4 entries.
//   - The resolver URL must be https://. Plain http is rejected at
//     construction time (NewResolver). Tests use NewResolverWithClient
//     to point at an httptest.Server (http://) without the check.
//   - No DNSSEC validation, no caching, no retries — the focusd
//     scheduler retries the whole job.
package dns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout is the per-query HTTP timeout. The reconciler queries
// domains sequentially, so a tight timeout keeps the worst-case job
// runtime bounded (8 domains * 5s = 40s, under the 60s job timeout).
const DefaultTimeout = 5 * time.Second

// Resolver is a DoH client. Use NewResolver; the zero value is unusable.
type Resolver struct {
	endpoint string
	client   *http.Client
}

// NewResolver builds a Resolver pointed at endpoint, which MUST be an
// https:// URL. The constructor rejects anything else so a typo in
// config can't silently downgrade us to plaintext DNS.
func NewResolver(endpoint string) (*Resolver, error) {
	if endpoint == "" {
		return nil, errors.New("dns: resolver endpoint is empty")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("dns: parse resolver: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, fmt.Errorf("dns: resolver must be https://, got %q", u.Scheme)
	}
	return &Resolver{
		endpoint: endpoint,
		client:   &http.Client{Timeout: DefaultTimeout},
	}, nil
}

// NewResolverWithClient is the test seam used to point the Resolver at
// an httptest.Server. The endpoint scheme check is bypassed because
// httptest URLs are http://; that is only safe in tests, hence the
// dedicated constructor.
func NewResolverWithClient(endpoint string, c *http.Client) *Resolver {
	return &Resolver{endpoint: endpoint, client: c}
}

// dohResponse mirrors the application/dns-json wire format. Only the
// Answer section's name/type/data fields matter for our use case; other
// fields are tolerated and ignored.
type dohResponse struct {
	Status int `json:"Status"`
	Answer []struct {
		Name string `json:"name"`
		Type int    `json:"type"`
		TTL  int    `json:"TTL"`
		Data string `json:"data"`
	} `json:"Answer"`
}

const recordTypeA = 1

// maxBodyBytes caps the response body we read. Real DoH answers are a
// few hundred bytes; 1MiB is generous and bounds memory if a resolver
// misbehaves.
const maxBodyBytes = 1 << 20

// ResolveA queries the resolver for A records of name and returns the
// IPv4 addresses as strings (e.g. "1.2.3.4"). Order is preserved from
// the resolver response. AAAA / non-A answers and unparseable Data
// fields are skipped silently.
//
// An empty result with nil error is valid — it means the resolver
// answered cleanly but the domain has no A records right now. Callers
// must treat that as "no IPs to add", not "failure".
func (r *Resolver) ResolveA(ctx context.Context, name string) ([]string, error) {
	if r == nil || r.client == nil {
		return nil, errors.New("dns: resolver is nil")
	}
	if name == "" {
		return nil, errors.New("dns: name is empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("dns: build request: %w", err)
	}
	q := req.URL.Query()
	q.Set("name", name)
	q.Set("type", "A")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/dns-json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dns: query %s: %w", name, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("dns: read %s body: %w", name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dns: %s returned HTTP %d: %s",
			name, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed dohResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("dns: parse %s response: %w", name, err)
	}
	// Status != 0 means the resolver itself reported an error
	// (NXDOMAIN=3, SERVFAIL=2, etc.). Treat NXDOMAIN as "no A records"
	// (empty slice, nil error). Other statuses are real failures.
	if parsed.Status != 0 && parsed.Status != 3 {
		return nil, fmt.Errorf("dns: %s resolver Status=%d", name, parsed.Status)
	}

	out := make([]string, 0, len(parsed.Answer))
	for _, a := range parsed.Answer {
		if a.Type != recordTypeA {
			continue
		}
		ip := net.ParseIP(strings.TrimSpace(a.Data))
		if ip == nil {
			continue
		}
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		out = append(out, v4.String())
	}
	return out, nil
}
