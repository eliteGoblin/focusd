package infra

import (
	"strings"
	"testing"
)

func TestObfuscator_GenerateName(t *testing.T) {
	o := NewObfuscator()
	name := o.GenerateName()

	if name == "" {
		t.Error("expected non-empty name")
	}

	// Should look like a system process
	validPrefixes := []string{
		"com.apple.",
		"launchd.",
	}

	hasValidPrefix := false
	for _, prefix := range validPrefixes {
		if strings.HasPrefix(name, prefix) {
			hasValidPrefix = true
			break
		}
	}

	if !hasValidPrefix {
		t.Errorf("name '%s' doesn't have a valid system-like prefix", name)
	}
}

func TestObfuscator_GenerateName_Unique(t *testing.T) {
	o := NewObfuscator()
	names := make(map[string]bool)

	// Generate 100 names and check for uniqueness
	for i := 0; i < 100; i++ {
		name := o.GenerateName()
		if names[name] {
			t.Errorf("duplicate name generated: %s", name)
		}
		names[name] = true
	}
}

func TestObfuscator_GenerateName_Format(t *testing.T) {
	o := NewObfuscator()
	name := o.GenerateName()

	// Should have format: prefix.suffix.randomhex
	parts := strings.Split(name, ".")

	// At least 4 parts: com.apple.something.suffix.hex
	if len(parts) < 4 {
		t.Errorf("expected at least 4 parts in name, got %d: %s", len(parts), name)
	}

	// Last part should be hex (6 chars)
	lastPart := parts[len(parts)-1]
	if len(lastPart) != 6 {
		t.Errorf("expected 6-char hex suffix, got %d chars: %s", len(lastPart), lastPart)
	}
}
