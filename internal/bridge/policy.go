package bridge

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Policy defines runtime limits and guards for the bridge.
type Policy struct {
	MaxPerProject  int
	MaxGlobal      int
	MaxInputBytes  int
	AllowedPaths   []string // glob patterns for allowed repo_path values
}

// DefaultPolicy returns sensible defaults.
func DefaultPolicy() Policy {
	return Policy{
		MaxPerProject: 5,
		MaxGlobal:     20,
		MaxInputBytes: 65536,
	}
}

// ValidateRepoPath checks that the given path is under one of the allowed path patterns.
// If no patterns are configured, all paths are allowed.
func (p *Policy) ValidateRepoPath(repoPath string) error {
	if len(p.AllowedPaths) == 0 {
		return nil
	}
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	for _, pattern := range p.AllowedPaths {
		matched, err := filepath.Match(pattern, abs)
		if err != nil {
			continue
		}
		if matched {
			return nil
		}
		// Also check if abs starts with the pattern prefix (for directory trees)
		// e.g. pattern "/home/*/repos" should match "/home/mark/repos/myproject"
		patternDir := strings.TrimRight(pattern, "*?")
		if strings.HasPrefix(abs, patternDir) {
			return nil
		}
	}
	return fmt.Errorf("repo_path %q is not under any allowed path", repoPath)
}

// ValidateInput checks that input text does not exceed the maximum size.
func (p *Policy) ValidateInput(text string) error {
	if p.MaxInputBytes > 0 && len(text) > p.MaxInputBytes {
		return fmt.Errorf("input size %d exceeds max %d bytes", len(text), p.MaxInputBytes)
	}
	return nil
}

// CheckSessionLimits verifies that creating a new session would not exceed limits.
func (p *Policy) CheckSessionLimits(projectCount, globalCount int) error {
	if p.MaxPerProject > 0 && projectCount >= p.MaxPerProject {
		return fmt.Errorf("project session limit reached (%d/%d)", projectCount, p.MaxPerProject)
	}
	if p.MaxGlobal > 0 && globalCount >= p.MaxGlobal {
		return fmt.Errorf("global session limit reached (%d/%d)", globalCount, p.MaxGlobal)
	}
	return nil
}
