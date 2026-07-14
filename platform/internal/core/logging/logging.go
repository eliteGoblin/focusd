// Package logging provides the platform's structured logger. It is a
// thin wrapper over log/slog (stdlib, zero-dependency) writing to both
// stderr and a rotating-by-caller log file under the OS log dir.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// LogName is the platform's on-disk log basename. HF4 (FEATURE 24): neutral
// ("svc.log", not "platform.log") so a filesystem grep for 'platform' does not
// hit the log file. MUST match the daemon's platformsvc.PlatformLogName (the
// daemon redirects the child's stdio to the same file in the workdir).
const LogName = "svc.log"

// New builds a slog.Logger at the given level, teeing to stderr and, if
// logDir is non-empty, to <logDir>/svc.log.
func New(level, logDir string) (*slog.Logger, func() error, error) {
	w := io.Writer(os.Stderr)
	closer := func() error { return nil }

	if logDir != "" {
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("create log dir: %w", err)
		}
		f, err := os.OpenFile(filepath.Join(logDir, LogName),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file: %w", err)
		}
		w = io.MultiWriter(os.Stderr, f)
		closer = f.Close
	}

	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: parseLevel(level)})
	return slog.New(h), closer, nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
