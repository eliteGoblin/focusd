package osadapter

import (
	"strings"
	"testing"
	"time"
)

func TestPlistWorkerVsEnsurer(t *testing.T) {
	s := Spec{SelfPath: "/d/daemon", Workdir: "/wd", Github: "o/r",
		Asset: "platform-darwin-arm64", Interval: 30 * time.Second}

	a := Plist(s, RoleA)
	if !strings.Contains(a, "<string>com.focusd.daemon.a</string>") {
		t.Fatal("A label missing")
	}
	if !strings.Contains(a, "<key>KeepAlive</key><true/>") {
		t.Fatal("worker must have KeepAlive")
	}
	if !strings.Contains(a, "<string>run</string>") || !strings.Contains(a, "<string>--mesh</string>") {
		t.Fatal("worker args must include run + --mesh")
	}
	if strings.Contains(a, "StartInterval") {
		t.Fatal("worker must NOT have StartInterval")
	}

	e := Plist(s, RoleEnsure)
	if !strings.Contains(e, "<string>ensure</string>") {
		t.Fatal("ensurer must run the ensure subcommand")
	}
	if !strings.Contains(e, "<key>StartInterval</key><integer>30</integer>") {
		t.Fatalf("ensurer StartInterval wrong:\n%s", e)
	}
	if strings.Contains(e, "KeepAlive") {
		t.Fatal("ensurer must NOT have KeepAlive")
	}
}

func TestIntervalSecondsFloor(t *testing.T) {
	if got := intervalSeconds(Spec{Interval: 100 * time.Millisecond}); got != 1 {
		t.Fatalf("sub-second interval must floor to 1, got %d", got)
	}
	if got := intervalSeconds(Spec{Interval: 5 * time.Second}); got != 5 {
		t.Fatalf("got %d", got)
	}
}
