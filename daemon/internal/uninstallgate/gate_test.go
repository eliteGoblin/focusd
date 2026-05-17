package uninstallgate

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)

func TestPassagesEmbeddedAndDistinct(t *testing.T) {
	seen := map[string]bool{}
	for step := 1; step <= TotalSteps; step++ {
		p := Passage(step)
		if len(p) < 800 {
			t.Errorf("passage %d too short for ~5–10 min typing: %d chars", step, len(p))
		}
		if seen[p] {
			t.Errorf("passage %d is a duplicate", step)
		}
		seen[p] = true
	}
}

func TestEvaluateRatchet(t *testing.T) {
	cases := []struct {
		name     string
		s        State
		now      time.Time
		wantKind Kind
		wantStep int
	}{
		{"fresh → transcribe 1", State{}, t0, Transcribe, 1},
		{"after step1, still waiting", State{Step: 1, T1: t0}, t0.Add(Step1Wait - time.Minute), Wait, 0},
		{"after step1, wait elapsed → transcribe 2", State{Step: 1, T1: t0}, t0.Add(Step1Wait), Transcribe, 2},
		{"after step2, still waiting", State{Step: 2, T2: t0}, t0.Add(Step2Wait - time.Second), Wait, 0},
		{"after step2, wait elapsed → transcribe 3", State{Step: 2, T2: t0}, t0.Add(Step2Wait), Transcribe, 3},
		{"all done → proceed", State{Step: 3}, t0, Proceed, 0},
		{"over-complete → proceed", State{Step: 99}, t0, Proceed, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := Evaluate(c.s, c.now)
			if o.Kind != c.wantKind {
				t.Fatalf("kind = %v, want %v", o.Kind, c.wantKind)
			}
			if c.wantKind == Transcribe && o.Step != c.wantStep {
				t.Fatalf("step = %d, want %d", o.Step, c.wantStep)
			}
			if c.wantKind == Wait && o.Remaining <= 0 {
				t.Fatalf("wait remaining must be > 0, got %v", o.Remaining)
			}
		})
	}
}

func TestFullHappyPathWithFakeClock(t *testing.T) {
	now := t0
	s := State{}

	// Step 1
	if o := Evaluate(s, now); o.Kind != Transcribe || o.Step != 1 {
		t.Fatalf("want transcribe 1, got %+v", o)
	}
	s = Advance(s, now)
	if s.Step != 1 || !s.T1.Equal(now) {
		t.Fatalf("after advance 1: %+v", s)
	}

	// Too early for step 2.
	now = now.Add(Step1Wait - time.Minute)
	if o := Evaluate(s, now); o.Kind != Wait {
		t.Fatalf("want wait, got %+v", o)
	}
	// Advance is a no-op while waiting (idempotent / safe).
	if Advance(s, now) != s {
		t.Fatal("Advance during Wait must be a no-op")
	}

	// Step 2 after the 2h wait.
	now = t0.Add(Step1Wait)
	s = Advance(s, now)
	if s.Step != 2 || !s.T2.Equal(now) {
		t.Fatalf("after advance 2: %+v", s)
	}

	// Step 3 after the 4h wait.
	now = now.Add(Step2Wait)
	if o := Evaluate(s, now); o.Kind != Transcribe || o.Step != 3 {
		t.Fatalf("want transcribe 3, got %+v", o)
	}
	s = Advance(s, now)
	if s.Step != TotalSteps {
		t.Fatalf("after advance 3: %+v", s)
	}
	if o := Evaluate(s, now); o.Kind != Proceed {
		t.Fatalf("want proceed, got %+v", o)
	}
}

func TestAccept(t *testing.T) {
	ref := Passage(1)

	if ok, _ := Accept(ref, ref, MinTypingDuration-time.Second); ok {
		t.Fatal("must reject when submitted too fast (paste)")
	}
	if ok, msg := Accept(ref, ref, MinTypingDuration); !ok {
		t.Fatalf("exact transcription at min duration must pass, got %q", msg)
	}
	if ok, _ := Accept("nowhere near the passage text", ref, time.Hour); ok {
		t.Fatal("must reject a non-matching transcription")
	}
	if ok, _ := Accept("", ref, time.Hour); ok {
		t.Fatal("must reject empty transcription")
	}
}
