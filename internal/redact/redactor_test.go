package redact

import "testing"

func TestRedact(t *testing.T) {
	r, err := New([]string{`(?i)token\s*[:=]\s*\S+`, `(?i)password\s*[:=]\s*\S+`})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	in := "token=abc123 password:letmein safe=text"
	got := r.Redact(in)
	if got == in {
		t.Fatalf("expected redaction, got %q", got)
	}
	if got != "[REDACTED] [REDACTED] safe=text" {
		t.Fatalf("unexpected redacted text: %q", got)
	}
}

func TestNewInvalidPattern(t *testing.T) {
	if _, err := New([]string{"["}); err == nil {
		t.Fatal("expected invalid regex error")
	}
}
