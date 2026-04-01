package config

import (
	"testing"
	"time"
)

func TestShouldValidateStartupAndParseDuration(t *testing.T) {
	cfg := ProviderConfig{}
	if !cfg.ShouldValidateStartup() {
		t.Fatal("default ShouldValidateStartup was false")
	}

	disabled := false
	cfg.ValidateStartup = &disabled
	if cfg.ShouldValidateStartup() {
		t.Fatal("ShouldValidateStartup ignored false override")
	}

	d := ParseDuration("2s", time.Second)
	if d != 2*time.Second {
		t.Fatalf("ParseDuration=%v want 2s", d)
	}
	if got := ParseDuration("not-a-duration", 3*time.Second); got != 3*time.Second {
		t.Fatalf("ParseDuration invalid=%v want fallback 3s", got)
	}
}
