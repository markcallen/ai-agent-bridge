package bridge

import (
	"errors"
	"testing"
)

// TestInputSizeValidation verifies that WriteInput caps payloads at
// policy.MaxInputBytes (default 64 KB) and returns ErrInputTooLarge.
// Issue #153: input size validation.
func TestInputSizeValidation(t *testing.T) {
	tests := []struct {
		name      string
		maxBytes  int
		inputSize int
		wantErr   error
	}{
		{
			name:      "under limit is accepted",
			maxBytes:  100,
			inputSize: 50,
			wantErr:   nil,
		},
		{
			name:      "at limit is accepted",
			maxBytes:  100,
			inputSize: 100,
			wantErr:   nil,
		},
		{
			name:      "over limit returns ErrInputTooLarge",
			maxBytes:  100,
			inputSize: 101,
			wantErr:   ErrInputTooLarge,
		},
		{
			name:      "default 64KB limit accepts 64KB",
			maxBytes:  65536,
			inputSize: 65536,
			wantErr:   nil,
		},
		{
			name:      "default 64KB limit rejects 64KB+1",
			maxBytes:  65536,
			inputSize: 65537,
			wantErr:   ErrInputTooLarge,
		},
		{
			name:      "zero maxBytes means unlimited",
			maxBytes:  0,
			inputSize: 1 << 20, // 1 MB
			wantErr:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := Policy{MaxInputBytes: tc.maxBytes}
			data := make([]byte, tc.inputSize)

			err := policy.ValidateInputBytes(data)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("ValidateInputBytes: unexpected error: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateInputBytes error=%v want %v", err, tc.wantErr)
			}
		})
	}
}

// TestInputSizeValidationText verifies ValidateInput (string variant).
func TestInputSizeValidationText(t *testing.T) {
	tests := []struct {
		name      string
		maxBytes  int
		inputSize int
		wantErr   error
	}{
		{
			name:      "text under limit is accepted",
			maxBytes:  10,
			inputSize: 10,
			wantErr:   nil,
		},
		{
			name:      "text over limit returns ErrInputTooLarge",
			maxBytes:  10,
			inputSize: 11,
			wantErr:   ErrInputTooLarge,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := Policy{MaxInputBytes: tc.maxBytes}
			text := string(make([]byte, tc.inputSize))

			err := policy.ValidateInput(text)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("ValidateInput: unexpected error: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateInput error=%v want %v", err, tc.wantErr)
			}
		})
	}
}

// TestSessionLimitsPerProject verifies that max_per_project is enforced.
// Issue #153: session limits.
func TestSessionLimitsPerProject(t *testing.T) {
	tests := []struct {
		name         string
		maxProject   int
		maxGlobal    int
		projectCount int
		globalCount  int
		wantErr      error
	}{
		{
			name:         "under project limit is allowed",
			maxProject:   5,
			maxGlobal:    20,
			projectCount: 4,
			globalCount:  4,
			wantErr:      nil,
		},
		{
			name:         "at project limit is rejected",
			maxProject:   5,
			maxGlobal:    20,
			projectCount: 5,
			globalCount:  5,
			wantErr:      ErrSessionLimitReached,
		},
		{
			name:         "over project limit is rejected",
			maxProject:   2,
			maxGlobal:    20,
			projectCount: 3,
			globalCount:  5,
			wantErr:      ErrSessionLimitReached,
		},
		{
			name:         "under global limit but at project limit is rejected",
			maxProject:   1,
			maxGlobal:    100,
			projectCount: 1,
			globalCount:  1,
			wantErr:      ErrSessionLimitReached,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := Policy{MaxPerProject: tc.maxProject, MaxGlobal: tc.maxGlobal}
			err := policy.CheckSessionLimits(tc.projectCount, tc.globalCount)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("CheckSessionLimits: unexpected error: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckSessionLimits error=%v want %v", err, tc.wantErr)
			}
		})
	}
}

// TestSessionLimitsGlobal verifies that max_global is enforced.
// Issue #153: session limits.
func TestSessionLimitsGlobal(t *testing.T) {
	tests := []struct {
		name         string
		maxProject   int
		maxGlobal    int
		projectCount int
		globalCount  int
		wantErr      error
	}{
		{
			name:         "under global limit is allowed",
			maxProject:   10,
			maxGlobal:    5,
			projectCount: 0,
			globalCount:  4,
			wantErr:      nil,
		},
		{
			name:         "at global limit is rejected",
			maxProject:   10,
			maxGlobal:    5,
			projectCount: 0,
			globalCount:  5,
			wantErr:      ErrSessionLimitReached,
		},
		{
			name:         "over global limit is rejected",
			maxProject:   100,
			maxGlobal:    2,
			projectCount: 0,
			globalCount:  3,
			wantErr:      ErrSessionLimitReached,
		},
		{
			name:         "zero maxGlobal means unlimited",
			maxProject:   100,
			maxGlobal:    0,
			projectCount: 0,
			globalCount:  10000,
			wantErr:      nil,
		},
		{
			name:         "zero maxProject means unlimited",
			maxProject:   0,
			maxGlobal:    100,
			projectCount: 10000,
			globalCount:  0,
			wantErr:      nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := Policy{MaxPerProject: tc.maxProject, MaxGlobal: tc.maxGlobal}
			err := policy.CheckSessionLimits(tc.projectCount, tc.globalCount)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("CheckSessionLimits: unexpected error: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckSessionLimits error=%v want %v", err, tc.wantErr)
			}
		})
	}
}
