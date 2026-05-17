// Package uninstallgate is the commitment-device friction in front of
// `daemon uninstall` (prod / user+system only — the e2e build bypasses
// it so CI teardown never blocks).
//
// The threat is the USER in a weak moment: an impulse to rip out their
// own focus protection that, left a few hours, would fade. The gate turns
// an instant, consequence-free removal into a deliberate, slow, multi-
// hour ritual that only a calm, determined version of the user will
// complete.
//
//	step 1: transcribe passage A (~5–10 min)        → wait 2h
//	step 2: transcribe passage B (after the 2h wait) → wait 4h
//	step 3: transcribe passage C (after the 4h wait) → uninstall proceeds
//
// State lives in one HMAC-signed file at a deterministic per-mode path.
// Any tamper (bad HMAC / corrupt / missing) or a backwards clock simply
// resets the user to step 1 — editing the file only costs the user their
// own progress, so cheating is self-defeating. This is CASUAL-GRADE by
// design: the binary is open source, so a determined root user can read
// the secret/passages and bypass it. That is the accepted honest ceiling
// (see daemon_design.md); real, unforgeable enforcement is the future
// off-box server's job (design decision D11). The durable lever here is
// the multi-hour real-time delay, not cryptographic strength.
package uninstallgate

import (
	"embed"
	"time"
)

// Tunables. The waits are anchored to each step's own completion time.
const (
	Step1Wait           = 2 * time.Hour
	Step2Wait           = 4 * time.Hour
	TotalSteps          = 3
	SimilarityThreshold = 0.97             // ≥97% normalized → accept (typo-forgiving)
	MinTypingDuration   = 60 * time.Second // submitted faster ⇒ almost certainly pasted
)

//go:embed passages/step1.txt passages/step2.txt passages/step3.txt
var passageFS embed.FS

var passageFiles = [TotalSteps]string{
	"passages/step1.txt", "passages/step2.txt", "passages/step3.txt",
}

// Passage returns the reference text the user must transcribe for a step
// (1-based). It panics only on a programming error (bad embed), never on
// user input.
func Passage(step int) string {
	b, err := passageFS.ReadFile(passageFiles[step-1])
	if err != nil {
		panic("uninstallgate: embedded passage missing: " + err.Error())
	}
	return string(b)
}

// State is the persisted progress. Step is the number of steps already
// completed (0..3). T1/T2 are when steps 1/2 were completed; the waits
// are measured from them. LastSeen is the wall clock at the last write,
// used purely to detect a backwards clock.
type State struct {
	Step     int       `json:"step"`
	T1       time.Time `json:"t1,omitempty"`
	T2       time.Time `json:"t2,omitempty"`
	LastSeen time.Time `json:"last_seen"`
}

// Kind is what the caller should do next.
type Kind int

const (
	// Transcribe: prompt the user with Passage(Step+1); on a good
	// transcription call Advance.
	Transcribe Kind = iota
	// Wait: the required cool-off has not elapsed; show Remaining.
	Wait
	// Proceed: all three steps done — perform the real uninstall.
	Proceed
)

// Outcome is the result of Evaluate.
type Outcome struct {
	Kind      Kind
	Step      int           // 1-based step the user is on (for Transcribe)
	Remaining time.Duration // time left (for Wait)
}

// Evaluate is the pure decision: given trusted state and the current
// time, what happens next. (Tamper/rollback are handled at Load, which
// hands Evaluate a zeroed state — i.e. "start from step 1".)
func Evaluate(s State, now time.Time) Outcome {
	switch s.Step {
	case 0:
		return Outcome{Kind: Transcribe, Step: 1}
	case 1:
		if d := s.T1.Add(Step1Wait).Sub(now); d > 0 {
			return Outcome{Kind: Wait, Remaining: d}
		}
		return Outcome{Kind: Transcribe, Step: 2}
	case 2:
		if d := s.T2.Add(Step2Wait).Sub(now); d > 0 {
			return Outcome{Kind: Wait, Remaining: d}
		}
		return Outcome{Kind: Transcribe, Step: 3}
	default: // >= TotalSteps
		return Outcome{Kind: Proceed}
	}
}

// Advance records a successfully transcribed step. It only moves forward
// when Evaluate currently asks for that exact transcription, so a replay
// or an out-of-order call is a no-op (idempotent / safe).
func Advance(s State, now time.Time) State {
	o := Evaluate(s, now)
	if o.Kind != Transcribe {
		return s
	}
	switch o.Step {
	case 1:
		s.Step, s.T1 = 1, now
	case 2:
		s.Step, s.T2 = 2, now
	case 3:
		s.Step = 3
	}
	s.LastSeen = now
	return s
}

// Accept reports whether a transcription is good enough to advance:
// it must take at least MinTypingDuration (instant ⇒ pasted) and be at
// least SimilarityThreshold similar to the reference passage.
func Accept(typed, reference string, elapsed time.Duration) (bool, string) {
	if elapsed < MinTypingDuration {
		return false, "submitted too fast — type the passage out by hand"
	}
	if similarity(typed, reference) < SimilarityThreshold {
		return false, "transcription does not match closely enough — try again"
	}
	return true, ""
}
