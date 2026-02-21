package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := strings.Join([]string{
		"# comment",
		"DOTENV_TEST_FOO=bar",
		`DOTENV_TEST_BAR="baz qux"`,
		"export DOTENV_TEST_ZED=1",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("DOTENV_TEST_KEEP", "existing")
	os.Unsetenv("DOTENV_TEST_FOO")
	os.Unsetenv("DOTENV_TEST_BAR")
	os.Unsetenv("DOTENV_TEST_ZED")

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}

	if got := os.Getenv("DOTENV_TEST_FOO"); got != "bar" {
		t.Fatalf("DOTENV_TEST_FOO=%q, want %q", got, "bar")
	}
	if got := os.Getenv("DOTENV_TEST_BAR"); got != "baz qux" {
		t.Fatalf("DOTENV_TEST_BAR=%q, want %q", got, "baz qux")
	}
	if got := os.Getenv("DOTENV_TEST_ZED"); got != "1" {
		t.Fatalf("DOTENV_TEST_ZED=%q, want %q", got, "1")
	}
	if got := os.Getenv("DOTENV_TEST_KEEP"); got != "existing" {
		t.Fatalf("DOTENV_TEST_KEEP=%q, want %q", got, "existing")
	}
}

func TestLoadDotEnvDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("DOTENV_TEST_FOO=from-file\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("DOTENV_TEST_FOO", "from-env")

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if got := os.Getenv("DOTENV_TEST_FOO"); got != "from-env" {
		t.Fatalf("DOTENV_TEST_FOO=%q, want %q", got, "from-env")
	}
}

func TestValidateProviderEnv(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"claude": {RequiredEnv: []string{"ANTHROPIC_API_KEY"}},
		},
	}

	t.Setenv("ANTHROPIC_API_KEY", "")
	err := ValidateProviderEnv(cfg)
	if err == nil {
		t.Fatal("expected missing env error")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Setenv("ANTHROPIC_API_KEY", "x")
	if err := ValidateProviderEnv(cfg); err != nil {
		t.Fatalf("ValidateProviderEnv: %v", err)
	}
}
