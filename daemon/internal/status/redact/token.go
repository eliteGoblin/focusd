// Package redact carries disguised focusd identifiers (the workdir path,
// launchd labels, daemon binary filename, pf anchor name) in a type that
// CANNOT print its raw value.
//
// This is the structural half of ADR-0011: `daemon status` exists to
// answer "is focusd working?" without leaking the strings a weak-moment
// self needs for `launchctl bootout` / `pfctl -F` / `rm`. A renderer that
// "remembers" to strip secrets is best-effort and fails the moment one
// new field or error path prints the raw value. Instead, disguised values
// live in a Token whose raw form is unexported and has NO accessor: code
// holding a Token can stat/launchctl/pfctl with it internally (via Use),
// but every externally-visible rendering — String, fmt %v/%s, JSON — emits
// "<redacted>". Leaking a token becomes a mistake you'd have to go out of
// your way to write, not one you trip into.
package redact

import "encoding/json"

// Placeholder is the single visible form of any redacted value.
const Placeholder = "<redacted>"

// Token wraps a disguised identifier. The zero value is an empty,
// "absent" token (Present reports false). The raw string is unexported
// and intentionally has no exported accessor — see Use for the only
// internal escape hatch.
type Token struct {
	raw string
}

// New wraps raw in a Token. raw may be empty (an absent token).
func New(raw string) Token { return Token{raw: raw} }

// Present reports whether the token holds a non-empty value. Useful for
// "is there a workdir?" decisions WITHOUT exposing the value itself.
func (t Token) Present() bool { return t.raw != "" }

// String renders the token as the redaction placeholder, never the raw
// value. This is what fmt's %s/%v and any string concatenation see, so a
// stray `fmt.Sprintf("%s", tok)` is safe by construction.
func (t Token) String() string { return Placeholder }

// GoString covers the %#v verb too, so even debug-style formatting of a
// struct containing a Token cannot leak the raw value.
func (t Token) GoString() string { return Placeholder }

// MarshalJSON emits the redaction placeholder for present tokens and an
// empty string for absent ones — so a Token embedded anywhere in a JSON
// snapshot can never carry the raw identifier into machine output.
func (t Token) MarshalJSON() ([]byte, error) {
	if t.raw == "" {
		return json.Marshal("")
	}
	return json.Marshal(Placeholder)
}

// Use is the ONLY way to read the raw value, and it never returns it —
// it hands the raw string to fn and returns whatever fn computes. Probes
// call Use to stat the workdir / launchctl-print a label / pfctl a table,
// reducing the raw value to a primitive (count, bool, age) that is safe
// to render. The raw string never escapes this call.
func Use[T any](t Token, fn func(raw string) T) T { return fn(t.raw) }
