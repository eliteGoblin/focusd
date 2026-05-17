package uninstallgate

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/eliteGoblin/focusd/daemon/internal/mode"
)

// hmacSecret authenticates the on-disk state so a casual hand-edit of
// the file is detected. It is COMPILED IN and the project is open source,
// so this is explicitly casual-grade: a determined user reading the
// source can forge it. That is the accepted honest ceiling — the real
// lever is the multi-hour delay, and unforgeable enforcement is the
// future server's job (D11). Detecting the casual edit is enough, because
// a detected edit just resets the user to step 1 (see Load).
var hmacSecret = []byte("focusd/uninstallgate/v1 — casual integrity tag, not a security boundary")

// stateFile is the deterministic, disguised filename the gate state is
// stored under (per-mode, next to nothing else). Deterministic on
// purpose: the 3 invocations over ~6h must find it without a scan.
const stateFile = ".com.apple.diagnostics.ug"

// StatePath is where the gate state lives for an install mode: a single
// hidden file under that mode's Application Support root (user →
// ~/Library, system → /Library), so user and system installs never share
// gate state.
func StatePath(m mode.Mode, home string) string {
	return filepath.Join(mode.SupportRoot(m, home), stateFile)
}

// envelope is what is actually written: the JSON state plus an HMAC over
// that exact JSON.
type envelope struct {
	State json.RawMessage `json:"state"`
	MAC   string          `json:"mac"`
}

func mac(payload []byte) string {
	h := hmac.New(sha256.New, hmacSecret)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// Load reads the gate state. ANY failure — missing file, unreadable,
// corrupt JSON, HMAC mismatch (hand-edited), or a clock that moved
// backwards since the last write — returns the zero State, i.e. "start
// over from step 1". Tampering therefore only costs the user their own
// progress; it never advances them and never hard-blocks.
func Load(path string, now time.Time) State {
	raw, err := os.ReadFile(path)
	if err != nil {
		return State{}
	}
	var env envelope
	if json.Unmarshal(raw, &env) != nil {
		return State{}
	}
	if !hmac.Equal([]byte(env.MAC), []byte(mac(env.State))) {
		return State{} // hand-edited / corrupt → reset
	}
	var s State
	if json.Unmarshal(env.State, &s) != nil {
		return State{}
	}
	if !s.LastSeen.IsZero() && now.Before(s.LastSeen) {
		return State{} // clock rolled back → reset
	}
	return s
}

// Save writes the state HMAC-signed, 0600, creating the parent dir. The
// caller is expected to have set LastSeen (Advance does).
func Save(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(s)
	if err != nil {
		return err
	}
	out, err := json.Marshal(envelope{State: payload, MAC: mac(payload)})
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// Clear removes the gate state (on a completed uninstall, or --abort).
// Missing file is success.
func Clear(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
