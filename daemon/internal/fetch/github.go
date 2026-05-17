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
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
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
		return "", err
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("fetch/github: empty tag")
	}
	return rel.TagName, nil
}

func (g *GitHub) EnsureBinary(ctx context.Context, st *core.Store, version string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", g.Repo, version)
	resp, err := g.get(ctx, url)
	if err != nil {
		return fmt.Errorf("fetch/github: release %s: %w", version, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("fetch/github: release %s status %d", version, resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return err
	}
	dlURL := ""
	for _, a := range rel.Assets {
		if a.Name == g.Asset {
			dlURL = a.URL
			break
		}
	}
	if dlURL == "" {
		return fmt.Errorf("fetch/github: asset %q not in release %s", g.Asset, version)
	}

	tmp, err := os.CreateTemp("", "focusd-dl-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	dl, err := g.get(ctx, dlURL)
	if err != nil {
		tmp.Close()
		return err
	}
	defer dl.Body.Close()
	if dl.StatusCode != 200 {
		tmp.Close()
		return fmt.Errorf("fetch/github: download status %d", dl.StatusCode)
	}
	if _, err := io.Copy(tmp, dl.Body); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	ok, err := sig.VerifyFile(tmpPath)
	if err != nil {
		return fmt.Errorf("fetch/github: verify: %w", err)
	}
	if !ok {
		return fmt.Errorf("fetch/github: %s failed signature verification — refusing", version)
	}
	return placeVerified(tmpPath, st.BinPath(version))
}
