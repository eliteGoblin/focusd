package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug, "DEBUG": slog.LevelDebug,
		"warn": slog.LevelWarn, "warning": slog.LevelWarn,
		"error": slog.LevelError, "info": slog.LevelInfo,
		"": slog.LevelInfo, "bogus": slog.LevelInfo,
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewWritesToFile(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := New("debug", dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	log.Info("hello", "k", "v")
	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "platform.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(b) == 0 {
		t.Error("log file is empty")
	}
}

func TestNewNoFileWhenDirEmpty(t *testing.T) {
	log, closer, err := New("info", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	log.Info("stderr only")
	if err := closer(); err != nil {
		t.Errorf("closer: %v", err)
	}
}
