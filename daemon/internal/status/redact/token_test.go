package redact

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// poison is a real-looking disguised value: if any rendering path leaks
// it, these tests fail.
const poison = "/Library/Application Support/.com.apple.metadata.7f3a2c/com.brave.Browser.helper.ab12cd34ef"

func TestStringNeverLeaks(t *testing.T) {
	tok := New(poison)
	if got := tok.String(); got != Placeholder {
		t.Fatalf("String() = %q, want %q", got, Placeholder)
	}
	// fmt verbs that route through Stringer/GoStringer must also be safe.
	for _, verb := range []string{"%s", "%v", "%+v", "%#v"} {
		out := fmt.Sprintf(verb, tok)
		if strings.Contains(out, "Library") || strings.Contains(out, "apple") {
			t.Fatalf("fmt %s leaked raw value: %q", verb, out)
		}
	}
}

func TestStructFormattingNeverLeaks(t *testing.T) {
	// A Token embedded in a struct must not leak via %v/%+v/%#v either.
	type holder struct{ W Token }
	h := holder{W: New(poison)}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		out := fmt.Sprintf(verb, h)
		if strings.Contains(out, "Library") || strings.Contains(out, "apple") {
			t.Fatalf("struct fmt %s leaked raw value: %q", verb, out)
		}
	}
}

func TestJSONNeverLeaks(t *testing.T) {
	type holder struct {
		W Token `json:"w"`
	}
	b, err := json.Marshal(holder{W: New(poison)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Library") || strings.Contains(string(b), "apple") {
		t.Fatalf("JSON leaked raw value: %s", b)
	}
	// json.Marshal HTML-escapes < and > to </>, so check for the
	// stable inner token rather than the literal angle-bracket placeholder.
	if !strings.Contains(string(b), "redacted") {
		t.Fatalf("JSON should contain redaction marker, got: %s", b)
	}
}

func TestAbsentToken(t *testing.T) {
	var z Token
	if z.Present() {
		t.Fatal("zero token should not be Present")
	}
	if New("").Present() {
		t.Fatal("empty token should not be Present")
	}
	if !New("x").Present() {
		t.Fatal("non-empty token should be Present")
	}
	// Absent token marshals to "" (not the placeholder) so machine output
	// can distinguish "no value" from "redacted value".
	b, _ := json.Marshal(struct {
		W Token `json:"w"`
	}{})
	if string(b) != `{"w":""}` {
		t.Fatalf("absent token JSON = %s, want {\"w\":\"\"}", b)
	}
}

func TestUseReadsRawInternally(t *testing.T) {
	tok := New(poison)
	// Use is the only escape hatch and returns a derived primitive, not
	// the raw string.
	n := Use(tok, func(raw string) int { return len(raw) })
	if n != len(poison) {
		t.Fatalf("Use computed len %d, want %d", n, len(poison))
	}
	gotRaw := Use(tok, func(raw string) bool { return raw == poison })
	if !gotRaw {
		t.Fatal("Use should hand the raw value to fn")
	}
}
