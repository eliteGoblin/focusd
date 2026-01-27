package infra

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestGitHubDownloader_TimeoutConstants verifies that timeout constants are properly separated.
// This is a regression test for the bug where the 30s client timeout conflicted with
// the 5-minute download context, causing slow downloads to fail.
func TestGitHubDownloader_TimeoutConstants(t *testing.T) {
	// Verify API timeout is reasonable (30 seconds)
	assert.Equal(t, 30*time.Second, githubAPITimeout,
		"API timeout should be 30 seconds for quick API calls")

	// Verify download timeout is much longer (5 minutes)
	assert.Equal(t, 5*time.Minute, downloadTimeout,
		"Download timeout should be 5 minutes for large asset downloads")

	// Verify download timeout is greater than API timeout
	assert.Greater(t, downloadTimeout, githubAPITimeout,
		"Download timeout should be greater than API timeout")
}

// TestNewGitHubDownloader_NoClientTimeout verifies that the client has no timeout.
// Timeouts are controlled per-request via context instead.
func TestNewGitHubDownloader_NoClientTimeout(t *testing.T) {
	downloader := NewGitHubDownloader()

	// Client should have no timeout (0 means no timeout)
	assert.Equal(t, time.Duration(0), downloader.client.Timeout,
		"Client should have no timeout; use context timeouts instead")
}

// TestNewGitHubDownloader_DefaultValues verifies default configuration.
func TestNewGitHubDownloader_DefaultValues(t *testing.T) {
	downloader := NewGitHubDownloader()

	assert.Equal(t, "eliteGoblin", downloader.owner)
	assert.Equal(t, "focusd", downloader.repo)
	assert.NotNil(t, downloader.client)
}
