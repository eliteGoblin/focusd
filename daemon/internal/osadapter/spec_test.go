package osadapter

import "testing"

func TestLabel(t *testing.T) {
	if (Spec{}).Label() != "com.focusd.daemon" {
		t.Fatal("default label wrong")
	}
	if (Spec{TestMode: true}).Label() != "com.focusd.daemon.e2e" {
		t.Fatal("test-mode label wrong")
	}
	if LabelFor(true) != "com.focusd.daemon.e2e" || LabelFor(false) != "com.focusd.daemon" {
		t.Fatal("LabelFor wrong")
	}
}
