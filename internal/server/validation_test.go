package server

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestSessionIDFormatValidation verifies that non-UUID session IDs are
// rejected with INVALID_ARGUMENT.
// Issue #153: session ID format validation.
func TestSessionIDFormatValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantOK  bool
		wantMsg string
	}{
		{
			name:   "valid UUID",
			value:  "550e8400-e29b-41d4-a716-446655440000",
			wantOK: true,
		},
		{
			name:   "valid UUID uppercase",
			value:  "550E8400-E29B-41D4-A716-446655440000",
			wantOK: true,
		},
		{
			name:    "empty string",
			value:   "",
			wantOK:  false,
			wantMsg: "session_id is required",
		},
		{
			name:    "plain text",
			value:   "not-a-uuid",
			wantOK:  false,
			wantMsg: "must be a valid UUID",
		},
		{
			name:    "numeric only",
			value:   "12345",
			wantOK:  false,
			wantMsg: "must be a valid UUID",
		},
		{
			name:   "UUID without dashes (accepted by google/uuid)",
			value:  "550e8400e29b41d4a716446655440000",
			wantOK: true,
		},
		{
			name:    "partial UUID",
			value:   "550e8400-e29b-41d4",
			wantOK:  false,
			wantMsg: "must be a valid UUID",
		},
		{
			name:    "UUID with extra chars",
			value:   "550e8400-e29b-41d4-a716-446655440000-extra",
			wantOK:  false,
			wantMsg: "must be a valid UUID",
		},
		{
			name:    "control characters",
			value:   "550e8400-e29b-41d4\x00a716-446655440000",
			wantOK:  false,
			wantMsg: "control characters",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUUIDField("session_id", tc.value)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("validateUUIDField(%q) unexpected error: %v", tc.value, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateUUIDField(%q) expected error, got nil", tc.value)
			}
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("validateUUIDField(%q) code=%v want %v", tc.value, got, codes.InvalidArgument)
			}
			if tc.wantMsg != "" {
				if msg := status.Convert(err).Message(); !strings.Contains(msg, tc.wantMsg) {
					t.Errorf("validateUUIDField(%q) message=%q, want substring %q", tc.value, msg, tc.wantMsg)
				}
			}
		})
	}
}

// TestByteFieldValidation verifies that oversized byte payloads are rejected.
// Issue #153: input size validation at the server layer.
func TestByteFieldValidation(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		maxLen  int
		wantErr bool
	}{
		{
			name:    "empty is rejected",
			data:    []byte{},
			maxLen:  100,
			wantErr: true,
		},
		{
			name:    "under limit is accepted",
			data:    []byte("hello"),
			maxLen:  100,
			wantErr: false,
		},
		{
			name:    "at limit is accepted",
			data:    make([]byte, 100),
			maxLen:  100,
			wantErr: false,
		},
		{
			name:    "over limit is rejected",
			data:    make([]byte, 101),
			maxLen:  100,
			wantErr: true,
		},
		{
			name:    "1MB limit rejects 1MB+1",
			data:    make([]byte, 1<<20+1),
			maxLen:  1 << 20,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Set first byte to non-zero for non-empty payloads to avoid "is required" error.
			if len(tc.data) > 0 {
				tc.data[0] = 'x'
			}
			err := validateByteField("data", tc.data, tc.maxLen)
			if tc.wantErr && err == nil {
				t.Fatalf("validateByteField: expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateByteField: unexpected error: %v", err)
			}
			if tc.wantErr && err != nil {
				if got := status.Code(err); got != codes.InvalidArgument {
					t.Fatalf("validateByteField code=%v want %v", got, codes.InvalidArgument)
				}
			}
		})
	}
}
