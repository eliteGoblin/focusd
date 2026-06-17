package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

// GitHub fetches signed release assets from a public GitHub repo.
// The downloaded asset is Ed25519-verified before it is placed.
type GitHub struct {
	Repo  string // "owner/name"
	Asset string // exact asset filename in the release (per os/arch)
	HTTP  *http.Client
}

func (g *GitHub) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

type ghRelease struct {
	TagName string `json:"tag_name"`
}

func (g *GitHub) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return g.client().Do(req)
}

func (g *GitHub) ResolveLatest(ctx context.Context) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", g.Repo)
	resp, err := g.get(ctx, url)
	if err != nil {
		return "", fmt.Errorf("fetch/github: latest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("fetch/github: latest status %d", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("fetch/github: latest: decode response: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("fetch/github: empty tag")
	}
	return rel.TagName, nil
}

// EnsureBinary downloads + Ed25519-verifies the configured asset for
// version and places it at st.BinPath(version). Thin wrapper around
// DownloadVerified, kept so existing reconcile callers don't change.
func (g *GitHub) EnsureBinary(ctx context.Context, st *core.Store, version string) error {
	return g.DownloadVerified(ctx, version, g.Asset, st.BinPath(version))
}

// DownloadVerified fetches asset `asset` from release `tag` of g.Repo
// via the DIRECT release-download URL, Ed25519-verifies it, and
// atomically writes the verified bytes (mode 0755) to dstPath. Returns
// nil only when the bytes at dstPath are signed by the embedded focusd
// public key.
//
// ADR-0015: the install is always pinned to a concrete tag, so the asset
// is reachable at
//
//	https://github.com/{repo}/releases/download/{tag}/{asset}
//
// served by GitHub's release CDN (302 → objects.githubusercontent.com).
// That path makes NO api.github.com call, so it is NOT subject to the
// 60-requests/hour unauthenticated REST rate limit. The previous
// implementation GET /releases/tags/{tag} on every reconcile tick (~2s),
// blowing through the limit in ~2 minutes → 403 on every later fetch →
// the platform binary never landed (the "fetch-storm"). ResolveLatest
// still uses the REST API, but only on the rare unpinned "latest" path.
//
// Used by both `daemon run` (place into <workdir>/bin/<v>/platform via
// EnsureBinary) and `daemon self-update` (place at a new rotated
// disguised binary basename in <workdir>). Pure boundary primitive —
// no launchd, no relocation, no Spec.
func (g *GitHub) DownloadVerified(ctx context.Context, tag, asset, dstPath string) error {
	dlURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", g.Repo, tag, asset)

	tmp, err := os.CreateTemp("", "focusd-dl-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	// Direct release download: dlURL 302s to signed objects.github CDN.
	// Use a PLAIN request — octet-stream Accept and NO Authorization
	// bearer forwarded to the redirect target (the repo is public, and a
	// bearer to the CDN/S3 leg can break the signed-URL download).
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		tmp.Close()
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	dl, err := g.client().Do(req)
	if err != nil {
		tmp.Close()
		return err
	}
	defer dl.Body.Close()
	if dl.StatusCode != 200 {
		tmp.Close()
		return fmt.Errorf("fetch/github: download status %d", dl.StatusCode)
	}
	// Cap the body so a malicious/misconfigured release can't push an
	// unbounded stream into the daemon.
	const maxAsset = 512 << 20 // 512 MiB ceiling
	if _, err := io.Copy(tmp, io.LimitReader(dl.Body, maxAsset)); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	ok, err := sig.VerifyFile(tmpPath)
	if err != nil {
		return fmt.Errorf("fetch/github: verify: %w", err)
	}
	if !ok {
		return fmt.Errorf("fetch/github: %s failed signature verification — refusing", tag)
	}
	return placeVerified(tmpPath, dstPath)
}
