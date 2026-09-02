package server

import (
	"testing"
	"time"
)

// TestRateLimitEnforcement verifies that the token bucket rate limiter
// rejects requests that exceed the configured RPS and burst.
// Issue #153: rate limiting.
func TestRateLimitEnforcement(t *testing.T) {
	tests := []struct {
		name       string
		rate       float64
		burst      int
		numCalls   int
		wantDenied bool
	}{
		{
			name:       "single call within burst is allowed",
			rate:       1,
			burst:      1,
			numCalls:   1,
			wantDenied: false,
		},
		{
			name:       "two calls with burst=1 denies second",
			rate:       1,
			burst:      1,
			numCalls:   2,
			wantDenied: true,
		},
		{
			name:       "burst=5 allows 5 immediate calls",
			rate:       1,
			burst:      5,
			numCalls:   5,
			wantDenied: false,
		},
		{
			name:       "burst=5 denies 6th immediate call",
			rate:       1,
			burst:      5,
			numCalls:   6,
			wantDenied: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			bucket := newTokenBucket(tc.rate, tc.burst, now)

			var denied bool
			for i := 0; i < tc.numCalls; i++ {
				if !bucket.allow(now) {
					denied = true
				}
			}

			if tc.wantDenied && !denied {
				t.Fatal("expected at least one denial, all calls were allowed")
			}
			if !tc.wantDenied && denied {
				t.Fatal("expected all calls allowed, but at least one was denied")
			}
		})
	}
}

// TestRateLimitTokenRefill verifies tokens refill over time.
func TestRateLimitTokenRefill(t *testing.T) {
	now := time.Now()
	bucket := newTokenBucket(10, 1, now) // 10 RPS, burst 1

	// Drain the single token.
	if !bucket.allow(now) {
		t.Fatal("first allow should succeed")
	}
	// Immediately after, should be denied.
	if bucket.allow(now) {
		t.Fatal("second immediate allow should fail")
	}
	// After 200ms at 10 RPS, 2 tokens should have refilled (only 1 needed).
	after := now.Add(200 * time.Millisecond)
	if !bucket.allow(after) {
		t.Fatal("allow after token refill should succeed")
	}
}

// TestKeyedLimiterIsolatesKeys verifies that different keys have independent buckets.
func TestKeyedLimiterIsolatesKeys(t *testing.T) {
	limiter := newKeyedLimiter(1, 1)

	// Key "a" uses its token.
	if !limiter.allow("a") {
		t.Fatal("first allow for key 'a' should succeed")
	}
	// Key "a" is now exhausted.
	if limiter.allow("a") {
		t.Fatal("second allow for key 'a' should fail")
	}
	// Key "b" should still have its token.
	if !limiter.allow("b") {
		t.Fatal("first allow for key 'b' should succeed")
	}
}

// TestCircuitBreakerTripsAfterThreshold verifies that the circuit breaker
// trips after circuitBreakerThreshold consecutive violations, banning the
// key for circuitBreakerBanDuration.
// Issue #153: rate limiting circuit breaker.
func TestCircuitBreakerTripsAfterThreshold(t *testing.T) {
	now := time.Now()
	bucket := newTokenBucket(1, 1, now)

	// Drain the only token so all subsequent calls are violations.
	bucket.allow(now)

	// Drive violations right up to the threshold.
	for i := 0; i < circuitBreakerThreshold-1; i++ {
		bucket.allow(now)
		if bucket.isBanned(now) {
			t.Fatalf("circuit breaker tripped too early at violation %d", i+1)
		}
	}

	// The next violation should trip the breaker.
	bucket.allow(now)
	if !bucket.isBanned(now) {
		t.Fatal("circuit breaker did not trip after threshold violations")
	}

	// During the ban, allow returns false.
	if bucket.allow(now.Add(time.Second)) {
		t.Fatal("allow should return false during ban")
	}

	// After the ban duration, the ban lifts.
	afterBan := now.Add(circuitBreakerBanDuration + time.Second)
	if bucket.isBanned(afterBan) {
		t.Fatal("bucket still banned after ban duration elapsed")
	}
}

// TestNilLimiterAllowsAll verifies that a nil keyedLimiter does not block.
func TestNilLimiterAllowsAll(t *testing.T) {
	var limiter *keyedLimiter
	if !limiter.allow("any-key") {
		t.Fatal("nil limiter should allow all")
	}
	if limiter.banned("any-key") {
		t.Fatal("nil limiter should never be banned")
	}
}

// TestZeroRateLimiterAllowsAll verifies that a zero-rate limiter allows all.
func TestZeroRateLimiterAllowsAll(t *testing.T) {
	limiter := newKeyedLimiter(0, 0)
	if !limiter.allow("any-key") {
		t.Fatal("zero-rate limiter should allow all")
	}
}
