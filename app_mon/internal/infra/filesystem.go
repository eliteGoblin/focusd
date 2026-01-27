package infra

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// FileSystemManagerImpl implements domain.FileSystemManager.
type FileSystemManagerImpl struct {
	homeDir string
}

// NewFileSystemManager creates a new filesystem manager.
func NewFileSystemManager() domain.FileSystemManager {
	home, _ := os.UserHomeDir()
	return &FileSystemManagerImpl{homeDir: home}
}

// NewFileSystemManagerWithHome creates a filesystem manager with custom home (for testing).
func NewFileSystemManagerWithHome(home string) domain.FileSystemManager {
	return &FileSystemManagerImpl{homeDir: home}
}

// Exists checks if a path exists.
func (fm *FileSystemManagerImpl) Exists(path string) bool {
	expanded := fm.ExpandHome(path)
	_, err := os.Stat(expanded)
	return err == nil
}

// Delete removes a file or directory recursively.
// Handles glob patterns in the path.
func (fm *FileSystemManagerImpl) Delete(path string) error {
	expanded := fm.ExpandHome(path)

	// Check if path contains glob patterns
	if strings.ContainsAny(expanded, "*?[") {
		return fm.deleteGlob(expanded)
	}

	// Direct path deletion
	return os.RemoveAll(expanded)
}

// deleteGlob handles deletion of paths matching glob patterns.
func (fm *FileSystemManagerImpl) deleteGlob(pattern string) error {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	var lastErr error
	for _, match := range matches {
		if err := os.RemoveAll(match); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// ExpandHome expands ~ to the user's home directory.
func (fm *FileSystemManagerImpl) ExpandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(fm.homeDir, path[2:])
	}
	if path == "~" {
		return fm.homeDir
	}
	return path
}

// Ensure FileSystemManagerImpl implements domain.FileSystemManager.
var _ domain.FileSystemManager = (*FileSystemManagerImpl)(nil)
