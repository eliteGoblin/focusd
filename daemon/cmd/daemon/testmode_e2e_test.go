//go:build e2e

package main

import (
	"flag"
	"testing"
)

// e2e builds opt the test install mode back in: --test-mode is
// registered and, when passed, requests test mode.
func TestTestModePresentInE2EBuild(t *testing.T) {
	if !testModeCompiledIn {
		t.Fatal("e2e build must compile in test mode")
	}
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	get := registerTestMode(fs)
	if fs.Lookup("test-mode") == nil {
		t.Fatal("e2e build must register --test-mode")
	}
	if get() {
		t.Fatal("default (flag unset) must be false")
	}
	if err := fs.Parse([]string{"--test-mode"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !get() {
		t.Fatal("--test-mode set must request test mode")
	}
}
