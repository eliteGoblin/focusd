package infra

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	githubOwner   = "eliteGoblin"
	githubRepo    = "focusd"
	githubAPIURL  = "https://api.github.com/repos/%s/%s/releases/latest"
	githubTimeout = 30 * time.Second
)

// GitHubRelease represents a GitHub release response.
type GitHubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []GitHubAsset `json:"assets"`
}

// GitHubAsset represents a release asset.
type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// GitHubDownloader downloads appmon binaries from GitHub releases.
type GitHubDownloader struct {
	client *http.Client
	owner  string
	repo   string
}

// NewGitHubDownloader creates a new GitHub downloader.
func NewGitHubDownloader() *GitHubDownloader {
	return &GitHubDownloader{
		client: &http.Client{Timeout: githubTimeout},
		owner:  githubOwner,
		repo:   githubRepo,
	}
}

// GetLatestRelease fetches the latest release info from GitHub.
func (d *GitHubDownloader) GetLatestRelease() (*GitHubRelease, error) {
	url := fmt.Sprintf(githubAPIURL, d.owner, d.repo)

	ctx, cancel := context.WithTimeout(context.Background(), githubTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "appmon")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse release: %w", err)
	}

	return &release, nil
}

// findAsset finds the matching asset for current platform.
func (d *GitHubDownloader) findAsset(release *GitHubRelease) (*GitHubAsset, error) {
	arch := runtime.GOARCH
	goos := runtime.GOOS

	// Look for matching asset
	for _, asset := range release.Assets {
		// Match pattern: appmon_X.X.X_darwin_arm64.tar.gz or appmon_darwin_arm64.tar.gz
		if strings.Contains(asset.Name, goos) && strings.Contains(asset.Name, arch) {
			return &asset, nil
		}
	}

	return nil, fmt.Errorf("no asset found for %s/%s", goos, arch)
}

// DownloadLatest downloads the latest release binary to the specified path.
func (d *GitHubDownloader) DownloadLatest(destPath string) error {
	// Get latest release
	release, err := d.GetLatestRelease()
	if err != nil {
		return fmt.Errorf("failed to get latest release: %w", err)
	}

	// Find matching asset
	asset, err := d.findAsset(release)
	if err != nil {
		return fmt.Errorf("failed to find asset: %w", err)
	}

	// Download the asset
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", asset.BrowserDownloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download asset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Create temp file for download
	tmpFile, err := os.CreateTemp("", "appmon-download-*.tar.gz")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// Write to temp file
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write download: %w", err)
	}
	tmpFile.Close()

	// Extract binary from tar.gz
	if err := d.extractBinary(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to extract binary: %w", err)
	}

	// Make executable
	if err := os.Chmod(destPath, 0755); err != nil {
		return fmt.Errorf("failed to chmod: %w", err)
	}

	return nil
}

// extractBinary extracts the appmon binary from a tar.gz archive.
func (d *GitHubDownloader) extractBinary(archivePath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Look for the appmon binary
		if header.Typeflag == tar.TypeReg &&
			(header.Name == "appmon" || strings.HasSuffix(header.Name, "/appmon")) {

			// Ensure destination directory exists
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}

			// Write binary to destination
			outFile, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer outFile.Close()

			if _, err := io.Copy(outFile, tr); err != nil {
				return err
			}

			return nil
		}
	}

	return fmt.Errorf("appmon binary not found in archive")
}

// DownloadToTemp downloads the latest release to a temp location and returns the path.
func (d *GitHubDownloader) DownloadToTemp() (string, error) {
	tmpDir, err := os.MkdirTemp("", "appmon-restore-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	destPath := filepath.Join(tmpDir, "appmon")
	if err := d.DownloadLatest(destPath); err != nil {
		os.RemoveAll(tmpDir)
		return "", err
	}

	return destPath, nil
}

// GetLatestVersion returns the version string of the latest release.
func (d *GitHubDownloader) GetLatestVersion() (string, error) {
	release, err := d.GetLatestRelease()
	if err != nil {
		return "", err
	}

	// Remove 'v' prefix if present
	version := strings.TrimPrefix(release.TagName, "v")
	return version, nil
}
