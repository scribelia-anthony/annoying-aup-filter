package fallback

import "testing"

func TestDefaultModel(t *testing.T) {
	f := New()
	if got := f.Model(); got != DefaultModel {
		t.Fatalf("default=%q want %q", got, DefaultModel)
	}
	if f.Enabled() {
		t.Fatal("must default to disabled")
	}
}

func TestSetAndSetModel(t *testing.T) {
	f := New()
	f.Set(true)
	if !f.Enabled() {
		t.Fatal("Set(true) didn't enable")
	}
	f.SetModel("claude-sonnet-4-6")
	if f.Model() != "claude-sonnet-4-6" {
		t.Fatalf("model=%q", f.Model())
	}
}

func TestSetModelIgnoresEmpty(t *testing.T) {
	f := New()
	f.SetModel("")
	if f.Model() != DefaultModel {
		t.Fatalf("empty SetModel clobbered default: %q", f.Model())
	}
}
