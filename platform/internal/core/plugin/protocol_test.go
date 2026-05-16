package plugin

import "testing"

func TestParseResult(t *testing.T) {
	r, err := ParseResult([]byte(`  {"status":"ok","message":"done","details":{"killed_count":2}}  ` + "\n"))
	if err != nil {
		t.Fatalf("ParseResult: %v", err)
	}
	if r.Status != "ok" || r.Message != "done" {
		t.Errorf("unexpected result %+v", r)
	}
	if r.Details["killed_count"].(float64) != 2 {
		t.Errorf("details not parsed: %+v", r.Details)
	}
}

func TestParseResultEmptyIsZeroValue(t *testing.T) {
	r, err := ParseResult([]byte("  \n\t "))
	if err != nil || r.Status != "" {
		t.Errorf("empty body should be zero Result, got %+v err=%v", r, err)
	}
}

func TestParseResultInvalid(t *testing.T) {
	if _, err := ParseResult([]byte("not json")); err == nil {
		t.Error("expected error for invalid JSON")
	}
}
