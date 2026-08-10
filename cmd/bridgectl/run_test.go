package main

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFormatStartSessionError_SetupCommandFailed(t *testing.T) {
	grpcErr := status.Errorf(codes.FailedPrecondition,
		"start session: repo setup failed: command failed: exit status 254; output: line one\nline two\nline three")

	got := formatStartSessionError(grpcErr)
	msg := got.Error()

	if !strings.HasPrefix(msg, "repo setup failed") {
		t.Fatalf("expected prefix 'repo setup failed', got: %s", msg)
	}
	if !strings.Contains(msg, "exit status 254") {
		t.Fatalf("expected exit status, got: %s", msg)
	}
	if !strings.Contains(msg, "    line one") {
		t.Fatalf("expected indented output, got: %s", msg)
	}
	if strings.Contains(msg, "rpc error") {
		t.Fatalf("should not contain gRPC framing, got: %s", msg)
	}
	if strings.Count(msg, "start session") > 0 {
		t.Fatalf("should not contain 'start session', got: %s", msg)
	}
}

func TestFormatStartSessionError_SetupTimeout(t *testing.T) {
	grpcErr := status.Errorf(codes.FailedPrecondition,
		"start session: repo setup failed: setup timed out after 2m0s")

	got := formatStartSessionError(grpcErr)
	msg := got.Error()

	if !strings.Contains(msg, "setup timed out after 2m0s") {
		t.Fatalf("expected timeout message, got: %s", msg)
	}
}

func TestFormatStartSessionError_NonSetupError(t *testing.T) {
	grpcErr := status.Errorf(codes.Unavailable, "provider unavailable")
	got := formatStartSessionError(grpcErr)
	msg := got.Error()

	if !strings.Contains(msg, "start session") {
		t.Fatalf("non-setup errors should keep 'start session' prefix, got: %s", msg)
	}
	if !strings.Contains(msg, "provider unavailable") {
		t.Fatalf("should preserve original message, got: %s", msg)
	}
}

func TestFormatStartSessionError_NonGRPC(t *testing.T) {
	plainErr := errors.New("connection refused")
	got := formatStartSessionError(plainErr)
	msg := got.Error()

	if !strings.HasPrefix(msg, "start session:") {
		t.Fatalf("non-gRPC errors should keep prefix, got: %s", msg)
	}
}
