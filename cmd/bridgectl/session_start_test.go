package main

import (
	"strings"
	"testing"
)

func TestSessionStartCmd_FlagDefaults(t *testing.T) {
	cmd := newSessionStartCmd()

	provider, err := cmd.Flags().GetString("provider")
	if err != nil {
		t.Fatalf("get provider flag: %v", err)
	}
	if provider != "claude" {
		t.Fatalf("default provider = %q, want %q", provider, "claude")
	}

	project, err := cmd.Flags().GetString("project")
	if err != nil {
		t.Fatalf("get project flag: %v", err)
	}
	if project != "local" {
		t.Fatalf("default project = %q, want %q", project, "local")
	}

	timeout, err := cmd.Flags().GetDuration("timeout")
	if err != nil {
		t.Fatalf("get timeout flag: %v", err)
	}
	if timeout.String() != "30m0s" {
		t.Fatalf("default timeout = %v, want 30m0s", timeout)
	}

	noTTY, err := cmd.Flags().GetBool("no-tty")
	if err != nil {
		t.Fatalf("get no-tty flag: %v", err)
	}
	if noTTY {
		t.Fatalf("default no-tty = %v, want false", noTTY)
	}

	remote, err := cmd.Flags().GetString("remote")
	if err != nil {
		t.Fatalf("get remote flag: %v", err)
	}
	if remote != "" {
		t.Fatalf("default remote = %q, want empty", remote)
	}
}

func TestSessionStartCmd_HasRemoteFlags(t *testing.T) {
	cmd := newSessionStartCmd()

	remoteFlags := []string{"remote", "cert", "key", "jwt-key", "server-name"}
	for _, name := range remoteFlags {
		f := cmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("missing flag: --%s", name)
		}
	}
}

func TestSessionStartCmd_AcceptsDirectoryArg(t *testing.T) {
	cmd := newSessionStartCmd()
	if cmd.Args == nil {
		t.Fatal("Args validator is nil")
	}
	// MaximumNArgs(1) should accept 0 or 1 args.
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Fatalf("0 args should be valid: %v", err)
	}
	if err := cmd.Args(cmd, []string{"."}); err != nil {
		t.Fatalf("1 arg should be valid: %v", err)
	}
	if err := cmd.Args(cmd, []string{".", "extra"}); err == nil {
		t.Fatal("2 args should be invalid")
	}
}

func TestSessionStartCmd_InvalidDirectory(t *testing.T) {
	cmd := newSessionStartCmd()
	cmd.SetArgs([]string{"/nonexistent-dir-for-bridgectl-test"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
	if !strings.Contains(err.Error(), "/nonexistent-dir-for-bridgectl-test") {
		t.Fatalf("error should reference the directory, got: %v", err)
	}
}

func TestRunCmd_IsDeprecated(t *testing.T) {
	cmd := newRunCmd()
	if cmd.Deprecated == "" {
		t.Fatal("run command should be marked as deprecated")
	}
	if !strings.Contains(cmd.Deprecated, "session start") {
		t.Fatalf("deprecation message should reference 'session start', got: %q", cmd.Deprecated)
	}
}

func TestRunCmd_LongDescriptionMentionsDeprecation(t *testing.T) {
	cmd := newRunCmd()
	if !strings.Contains(cmd.Long, "DEPRECATED") {
		t.Fatal("long description should mention DEPRECATED")
	}
	if !strings.Contains(cmd.Long, "session start") {
		t.Fatal("long description should mention 'session start' as replacement")
	}
}

func TestSessionStartCmd_ProviderShortFlag(t *testing.T) {
	cmd := newSessionStartCmd()
	f := cmd.Flags().ShorthandLookup("p")
	if f == nil {
		t.Fatal("missing short flag -p for provider")
	}
	if f.Name != "provider" {
		t.Fatalf("-p should map to 'provider', got %q", f.Name)
	}
}

func TestSessionStartCmd_TimeoutShortFlag(t *testing.T) {
	cmd := newSessionStartCmd()
	f := cmd.Flags().ShorthandLookup("t")
	if f == nil {
		t.Fatal("missing short flag -t for timeout")
	}
	if f.Name != "timeout" {
		t.Fatalf("-t should map to 'timeout', got %q", f.Name)
	}
}
