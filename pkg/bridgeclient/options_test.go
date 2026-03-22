package bridgeclient

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
)

func TestNew_MissingTarget(t *testing.T) {
	_, err := New()
	if err == nil {
		t.Fatal("expected error when target is missing, got nil")
	}
}

func TestNew_WithTarget(t *testing.T) {
	// A non-resolvable address is fine; New() uses grpc.NewClient which is lazy.
	c, err := New(WithTarget("localhost:19999"))
	if err != nil {
		t.Fatalf("New with target: %v", err)
	}
	_ = c.Close()
}

func TestNew_WithTimeout(t *testing.T) {
	c, err := New(
		WithTarget("localhost:19999"),
		WithTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("New with timeout: %v", err)
	}
	if c.timeout != 5*time.Second {
		t.Fatalf("timeout = %v, want 5s", c.timeout)
	}
	_ = c.Close()
}

func TestNew_WithRetry(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, InitialBackoff: 50 * time.Millisecond, MaxBackoff: 1 * time.Second}
	c, err := New(
		WithTarget("localhost:19999"),
		WithRetry(cfg),
	)
	if err != nil {
		t.Fatalf("New with retry: %v", err)
	}
	if c.retry.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts = %d, want 3", c.retry.MaxAttempts)
	}
	_ = c.Close()
}

func TestNew_RetryDefaults(t *testing.T) {
	// Passing a zero RetryConfig should be clamped to safe defaults.
	c, err := New(
		WithTarget("localhost:19999"),
		WithRetry(RetryConfig{}),
	)
	if err != nil {
		t.Fatalf("New with zero retry: %v", err)
	}
	if c.retry.MaxAttempts <= 0 {
		t.Fatalf("MaxAttempts should be > 0 after clamping, got %d", c.retry.MaxAttempts)
	}
	_ = c.Close()
}

func TestNew_WithCursorStore(t *testing.T) {
	store := NewMemoryCursorStore()
	c, err := New(
		WithTarget("localhost:19999"),
		WithCursorStore(store),
	)
	if err != nil {
		t.Fatalf("New with cursor store: %v", err)
	}
	if c.cursors != store {
		t.Fatal("cursor store not set correctly")
	}
	_ = c.Close()
}

func TestNew_DefaultCursorStore(t *testing.T) {
	// When no cursor store is provided a MemoryCursorStore is used.
	c, err := New(WithTarget("localhost:19999"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.cursors == nil {
		t.Fatal("expected default cursor store, got nil")
	}
	_ = c.Close()
}

func TestNew_WithCursorStoreNil(t *testing.T) {
	// Passing nil cursor store should fall back to MemoryCursorStore.
	c, err := New(
		WithTarget("localhost:19999"),
		WithCursorStore(nil),
	)
	if err != nil {
		t.Fatalf("New with nil cursor store: %v", err)
	}
	if c.cursors == nil {
		t.Fatal("expected fallback cursor store, got nil")
	}
	_ = c.Close()
}

func TestSetProject_NilJWT(t *testing.T) {
	c, err := New(WithTarget("localhost:19999"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	// SetProject should not panic when jwtCred is nil.
	c.SetProject("my-project")
}

func TestClient_Close(t *testing.T) {
	c, err := New(WithTarget("localhost:19999"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestShouldRetry(t *testing.T) {
	cases := []struct {
		code codes.Code
		want bool
	}{
		{codes.Unavailable, true},
		{codes.DeadlineExceeded, true},
		{codes.NotFound, false},
		{codes.Internal, false},
		{codes.OK, false},
	}
	for _, tc := range cases {
		err := grpcErr(tc.code, "test")
		got := shouldRetry(err)
		if got != tc.want {
			t.Errorf("shouldRetry(%v) = %v, want %v", tc.code, got, tc.want)
		}
	}
}
