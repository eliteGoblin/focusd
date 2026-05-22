package reconcile

import "testing"

func okWorld() Observed {
	return Observed{
		MyVersion: "v1", GoodVersion: "v1",
		PolicyApplied: true, PartnerAlive: true, LaunchEntriesOK: true,
	}
}

func never(string) bool { return false }

func TestDecideSteady(t *testing.T) {
	d := Decide(Desired{Version: "v1"}, okWorld(), false, never, DefaultVersionCompare)
	if !d.Steady || len(d.Actions) != 0 {
		t.Fatalf("expected steady, got %+v", d)
	}
}

func TestDecideTier0AlwaysEvaluated(t *testing.T) {
	o := okWorld()
	o.PolicyApplied = false
	o.PartnerAlive = false
	o.LaunchEntriesOK = false
	d := Decide(Desired{Version: "v1"}, o, false, never, DefaultVersionCompare)
	for _, a := range []Action{ActEnforcePolicy, ActRespawnPartner, ActRepairLaunch} {
		if !d.Has(a) {
			t.Errorf("missing tier-0 action %s in %+v", a, d.Actions)
		}
	}
	if d.Steady {
		t.Error("must not be steady when maintenance needed")
	}
}

func TestDecideCatchUpToGood(t *testing.T) {
	o := okWorld()
	o.MyVersion = "v1"
	o.GoodVersion = "v2" // good advanced; I'm behind
	d := Decide(Desired{Version: "v2"}, o, false, never, DefaultVersionCompare)
	if !d.Has(ActExitForUpgrade) || d.TargetVersion != "v2" {
		t.Fatalf("expected catch-up exit to v2, got %+v", d)
	}
}

func TestDecideCanaryNeedsLease(t *testing.T) {
	o := okWorld() // my == good == v1
	des := Desired{Version: "v2"}

	// No lease → safety net: NO version action, keep enforcing (steady).
	d := Decide(des, o, false, never, DefaultVersionCompare)
	if d.Has(ActExitForUpgrade) {
		t.Fatalf("safety-net must not upgrade without lease: %+v", d)
	}

	// Lease held → canary upgrades to desired.
	d = Decide(des, o, true, never, DefaultVersionCompare)
	if !d.Has(ActExitForUpgrade) || d.TargetVersion != "v2" {
		t.Fatalf("canary should exit to v2, got %+v", d)
	}
}

func TestDecideRefuseDowngrade(t *testing.T) {
	o := okWorld()
	o.GoodVersion = "v3"
	o.MyVersion = "v3"
	d := Decide(Desired{Version: "v2"}, o, true, never, DefaultVersionCompare)
	if !d.Has(ActRefuseDowngrade) || d.Has(ActExitForUpgrade) {
		t.Fatalf("must refuse downgrade and not upgrade: %+v", d)
	}
}

func TestDecideSkipBadVersion(t *testing.T) {
	o := okWorld()
	bad := func(v string) bool { return v == "v2" }
	d := Decide(Desired{Version: "v2"}, o, true, bad, DefaultVersionCompare)
	if !d.Has(ActSkipBadVersion) || d.Has(ActExitForUpgrade) {
		t.Fatalf("must skip bad version, stay on good: %+v", d)
	}
}

func TestDecideCombinedPartnerDeadDuringCanaryUpgrade(t *testing.T) {
	o := okWorld()
	o.PartnerAlive = false // partner died
	d := Decide(Desired{Version: "v2"}, o, true, never, DefaultVersionCompare)
	// Tier-0 (respawn) AND the version exit must both be present —
	// maintenance is never suppressed by a version transition.
	if !d.Has(ActRespawnPartner) || !d.Has(ActExitForUpgrade) {
		t.Fatalf("expected respawn + exit together, got %+v", d)
	}
}

func TestDecideFirstBootEmptyGood(t *testing.T) {
	o := Observed{MyVersion: "v1", GoodVersion: "", PolicyApplied: true,
		PartnerAlive: true, LaunchEntriesOK: true}
	// good == "" ⇒ trust my version; desired == my ⇒ steady, no churn.
	d := Decide(Desired{Version: "v1"}, o, false, never, DefaultVersionCompare)
	if !d.Steady {
		t.Fatalf("first boot with matching desired should be steady: %+v", d)
	}
	// Empty good + newer desired + lease ⇒ canary upgrade.
	d = Decide(Desired{Version: "v2"}, o, true, never, DefaultVersionCompare)
	if !d.Has(ActExitForUpgrade) || d.TargetVersion != "v2" {
		t.Fatalf("first boot canary upgrade expected: %+v", d)
	}
}

func TestDecideNoDesiredPinnedIsSteady(t *testing.T) {
	d := Decide(Desired{Version: ""}, okWorld(), false, never, DefaultVersionCompare)
	if !d.Steady {
		t.Fatalf("no desired version pinned ⇒ steady: %+v", d)
	}
}

func TestDecideHasHelper(t *testing.T) {
	d := Decision{Actions: []Action{ActEnforcePolicy}}
	if !d.Has(ActEnforcePolicy) || d.Has(ActRespawnPartner) {
		t.Error("Has() wrong")
	}
}

func TestDefaultVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1", "v2", -1},
		{"v2", "v1", 1},
		{"v1", "v1", 0},
		{"v1.10.0", "v1.9.0", 1}, // numeric, not lexical
		{"1.2.3", "1.2.3", 0},
		{"v1", "v1.0.0", 0}, // missing segments == 0
		{"v2.0", "v1.9.9", 1},
		{"main", "dev", 1}, // non-numeric → string compare ("main">"dev")
		{"v1", "dev", 1},   // one non-numeric → string compare ('v'>'d')
	}
	for _, c := range cases {
		if got := DefaultVersionCompare(c.a, c.b); got != c.want {
			t.Errorf("cmp(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}
