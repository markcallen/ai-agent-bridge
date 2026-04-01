package bridge

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"testing"
	"time"
)

type registryProvider struct {
	id        string
	healthErr error
}

func (p *registryProvider) ID() string                    { return p.id }
func (p *registryProvider) Binary() string                { return "/bin/true" }
func (p *registryProvider) PromptPattern() *regexp.Regexp { return nil }
func (p *registryProvider) StartupTimeout() time.Duration { return time.Second }
func (p *registryProvider) StopGrace() time.Duration      { return time.Second }
func (p *registryProvider) BuildCommand(context.Context, SessionConfig) (*exec.Cmd, error) {
	return exec.Command("/bin/true"), nil
}
func (p *registryProvider) ValidateStartup(context.Context) error { return nil }
func (p *registryProvider) Health(context.Context) error          { return p.healthErr }
func (p *registryProvider) Version(context.Context) (string, error) {
	return "v1", nil
}

func TestPolicyValidationAndRegistryHealth(t *testing.T) {
	repo := t.TempDir()
	policy := Policy{
		MaxPerProject: 1,
		MaxGlobal:     2,
		MaxInputBytes: 4,
		AllowedPaths:  []string{repo},
	}
	if err := policy.ValidateRepoPath(repo); err != nil {
		t.Fatalf("ValidateRepoPath: %v", err)
	}
	if err := policy.ValidateRepoPath(t.TempDir()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ValidateRepoPath disallowed error=%v want %v", err, ErrInvalidArgument)
	}
	if err := policy.ValidateInput("1234"); err != nil {
		t.Fatalf("ValidateInput: %v", err)
	}
	if err := policy.ValidateInput("12345"); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("ValidateInput oversized error=%v want %v", err, ErrInputTooLarge)
	}
	if err := policy.ValidateInputBytes([]byte("12345")); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("ValidateInputBytes oversized error=%v want %v", err, ErrInputTooLarge)
	}

	registry := NewRegistry()
	if err := registry.Register(&registryProvider{id: "healthy"}); err != nil {
		t.Fatalf("Register healthy: %v", err)
	}
	if err := registry.Register(&registryProvider{id: "broken", healthErr: errors.New("down")}); err != nil {
		t.Fatalf("Register broken: %v", err)
	}
	if got := registry.List(); len(got) != 2 {
		t.Fatalf("List len=%d want 2", len(got))
	}
	results := registry.HealthAll(context.Background())
	if results["healthy"] != nil || results["broken"] == nil {
		t.Fatalf("HealthAll=%v", results)
	}
}
