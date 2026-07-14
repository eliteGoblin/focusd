package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// FEATURE 25, Element 4 — the convergence/reap machinery is callable ONLY from
// installMesh / SelfUpdate / the reconcile tick, NEVER as a user-invocable CLI
// verb. A teardown verb would be exactly the impulsive off-switch the whole
// project exists to deny (mirrors uninstallgate_wiring_test.go: the sensitive
// path must not be reachable from argv).

// forbiddenVerbs are strings a weak-moment self might guess to try to converge
// the mesh down to zero. None may dispatch to anything.
var forbiddenVerbs = []string{
	"converge", "reap", "reap-platforms", "single-instance",
	"retire", "teardown", "kill-others", "converge-single-instance",
}

// TestNoConvergeVerbDispatches: run() must reject every convergence-shaped verb
// as unknown (exit code 2) — there is no CLI path into ConvergeSingleInstance /
// ReapForeignPlatforms / RetireOtherGenerations.
func TestNoConvergeVerbDispatches(t *testing.T) {
	for _, v := range forbiddenVerbs {
		if code := run([]string{v}); code != 2 {
			t.Fatalf("verb %q must be unknown (exit 2), got %d — it must NOT dispatch", v, code)
		}
	}
}

// caseVerbRE extracts the string literal from a `case "verb":` line.
var caseVerbRE = regexp.MustCompile(`case\s+"([^"]+)"`)

// TestRunSwitchHasNoConvergeVerb: structurally assert the run() dispatch switch
// exposes ONLY the known-safe verbs and none of the forbidden ones — so a future
// edit can't quietly wire a teardown verb in. The set is read from source so the
// guard tracks the actual dispatch table, not a copy.
func TestRunSwitchHasNoConvergeVerb(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	s := string(src)

	// Isolate the `switch args[0] { … }` block in run().
	start := strings.Index(s, "switch args[0] {")
	if start < 0 {
		t.Fatal("could not locate the run() dispatch switch in main.go")
	}
	// End at the switch's `default:` (the last case before the closing brace).
	end := strings.Index(s[start:], "default:")
	if end < 0 {
		t.Fatal("could not locate the switch default in main.go")
	}
	block := s[start : start+end]

	verbs := map[string]bool{}
	for _, m := range caseVerbRE.FindAllStringSubmatch(block, -1) {
		verbs[m[1]] = true
	}
	if len(verbs) == 0 {
		t.Fatal("no case verbs parsed — scan is broken")
	}

	// The known-safe dispatch table. A NEW verb here is a deliberate decision;
	// the test fails loudly until the allowlist is updated, forcing review.
	allowed := map[string]bool{
		"version": true, "-v": true, "--version": true,
		"run": true, "once": true, "update": true, "ensure": true,
		"install": true, "uninstall": true, "watchdog": true,
		"self-update": true, "status": true,
	}
	for v := range verbs {
		if !allowed[v] {
			t.Fatalf("unexpected CLI verb %q in run() switch — no new dispatch verbs without review", v)
		}
	}
	for _, bad := range forbiddenVerbs {
		if verbs[bad] {
			t.Fatalf("forbidden convergence/reap verb %q must NEVER be a CLI dispatch", bad)
		}
	}
}

// TestReapNotReachableFromCLIDispatch: ReapForeignPlatforms is wired only into
// the reconcile-loop executor (build) — never named inside the run() dispatch
// switch. ConvergeSingleInstance is wired only into installMesh. Assert neither
// identifier appears inside the dispatch switch block.
func TestReapNotReachableFromCLIDispatch(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	s := string(src)
	start := strings.Index(s, "switch args[0] {")
	end := strings.Index(s[start:], "default:")
	block := s[start : start+end]
	for _, id := range []string{"ConvergeSingleInstance", "ReapForeignPlatforms", "RetireOtherGenerations"} {
		if strings.Contains(block, id) {
			t.Fatalf("%s must not be invoked from the run() dispatch switch (it is not a verb)", id)
		}
	}
}
