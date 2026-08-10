package main

import (
	"context"
	"errors"
	"testing"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSessionExitMessage(t *testing.T) {
	tests := []struct {
		name string
		ev   *bridgev1.AttachSessionEvent
		want string
	}{
		{
			name: "exit recorded",
			ev: &bridgev1.AttachSessionEvent{
				ExitRecorded: true,
				ExitCode:     7,
			},
			want: "Session exited with code 7.",
		},
		{
			name: "exit not recorded",
			ev:   &bridgev1.AttachSessionEvent{},
			want: "Session exited.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionExitMessage(tt.ev); got != tt.want {
				t.Fatalf("sessionExitMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsCanceledStreamError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "context canceled",
			err:  context.Canceled,
			want: true,
		},
		{
			name: "wrapped context canceled",
			err:  errors.Join(errors.New("stream failed"), context.Canceled),
			want: true,
		},
		{
			name: "grpc canceled",
			err:  status.Error(codes.Canceled, "context canceled"),
			want: true,
		},
		{
			name: "grpc unavailable",
			err:  status.Error(codes.Unavailable, "transport closed"),
			want: false,
		},
		{
			name: "plain error",
			err:  errors.New("boom"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCanceledStreamError(tt.err); got != tt.want {
				t.Fatalf("isCanceledStreamError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
