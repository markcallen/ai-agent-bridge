package bridgeclient

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func grpcErr(code codes.Code, msg string) error {
	return status.Error(code, msg)
}

func TestMapError_Nil(t *testing.T) {
	if got := mapError(nil); got != nil {
		t.Fatalf("mapError(nil) = %v, want nil", got)
	}
}

func TestMapError_NotFound(t *testing.T) {
	err := mapError(grpcErr(codes.NotFound, "session not found"))
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
}

func TestMapError_AlreadyExists(t *testing.T) {
	err := mapError(grpcErr(codes.AlreadyExists, "exists"))
	if !errors.Is(err, ErrSessionAlreadyExists) {
		t.Fatalf("want ErrSessionAlreadyExists, got %v", err)
	}
}

func TestMapError_Unauthenticated(t *testing.T) {
	err := mapError(grpcErr(codes.Unauthenticated, "bad token"))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestMapError_PermissionDenied(t *testing.T) {
	err := mapError(grpcErr(codes.PermissionDenied, "denied"))
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("want ErrPermissionDenied, got %v", err)
	}
}

func TestMapError_ResourceExhausted_RateLimit(t *testing.T) {
	err := mapError(grpcErr(codes.ResourceExhausted, "rate limit exceeded"))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
}

func TestMapError_ResourceExhausted_SessionLimit(t *testing.T) {
	err := mapError(grpcErr(codes.ResourceExhausted, "session limit reached"))
	if !errors.Is(err, ErrSessionLimitReached) {
		t.Fatalf("want ErrSessionLimitReached, got %v", err)
	}
}

// codes.Unavailable is always passed through — both transport failures (TLS,
// connection refused, DNS, timeout, "all SubConns are in TransientFailure", …)
// and server-side "provider unavailable" arrive with this code. Callers need
// the original message to diagnose the problem.
func TestMapError_Unavailable_PassesThrough(t *testing.T) {
	cases := []string{
		"provider down",
		"transport: authentication handshake failed: tls: failed to verify certificate: x509: certificate signed by unknown authority",
		`connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:9445: connect: connection refused"`,
		`connection error: desc = "transport: Error while dialing: dial tcp: lookup macbook.ts.net: no such host"`,
		`connection error: desc = "transport: Error while dialing: dial tcp macbook.ts.net:9445: i/o timeout"`,
		`connection error: desc = "something unexpected happened"`,
		"all SubConns are in TransientFailure",
	}
	for _, msg := range cases {
		err := mapError(grpcErr(codes.Unavailable, msg))
		if err == nil {
			t.Fatalf("mapError(Unavailable, %q) = nil, want non-nil", msg)
		}
		if errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("mapError(Unavailable, %q) returned ErrProviderUnavailable sentinel; want raw gRPC error", msg)
		}
	}
}

func TestMapError_Unknown_PassThrough(t *testing.T) {
	orig := grpcErr(codes.Internal, "internal error")
	err := mapError(orig)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	// Unknown gRPC codes should pass through as-is (not wrapped into a sentinel).
	if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("unexpected sentinel: %v", err)
	}
}

func TestMapError_NonGRPC(t *testing.T) {
	orig := errors.New("plain error")
	err := mapError(orig)
	if err != orig {
		t.Fatalf("non-gRPC error should pass through unchanged, got %v", err)
	}
}
