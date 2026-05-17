//go:build !e2e

package main

import (
	"flag"
	"testing"
)

// Release builds must NOT compile in the test install mode, and the
// --test-mode flag must not be registered.
func TestTestModeAbsentInReleaseBuild(t *testing.T) {
	if testModeCompiledIn {
		t.Fatal("release build must not compile in test mode")
	}
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	get := registerTestMode(fs)
	if fs.Lookup("test-mode") != nil {
		t.Fatal("release build must not register --test-mode")
	}
	if get() {
		t.Fatal("release build must never request test mode")
	}
}
