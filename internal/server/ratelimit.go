package server

import (
	"sync"
	"time"
)

// circuitBreakerThreshold is the number of consecutive rate-limit violations
// that trips the circuit breaker for a single key.
const circuitBreakerThreshold = 100

// circuitBreakerBanDuration is how long a key stays banned after the breaker
// trips. During the ban, allow() returns false and banned() returns true so
// callers can take stronger action (e.g. terminate the session).
const circuitBreakerBanDuration = 5 * time.Minute

type tokenBucket struct {
	rate     float64
	burst    float64
	tokens   float64
	last     time.Time
	lastSeen time.Time

	// circuit breaker fields
	consecutiveViolations int
	bannedUntil           time.Time
}

func newTokenBucket(rate float64, burst int, now time.Time) *tokenBucket {
	return &tokenBucket{
		rate:     rate,
		burst:    float64(burst),
		tokens:   float64(burst),
		last:     now,
		lastSeen: now,
	}
}

// allow returns true when the request is within the rate limit. It also tracks
// consecutive violations and sets bannedUntil when the circuit breaker trips.
func (b *tokenBucket) allow(now time.Time) bool {
	b.lastSeen = now

	// While banned, every call counts as a denial.
	if now.Before(b.bannedUntil) {
		return false
	}

	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}

	if b.tokens < 1 {
		b.consecutiveViolations++
		if b.consecutiveViolations >= circuitBreakerThreshold {
			b.bannedUntil = now.Add(circuitBreakerBanDuration)
			b.consecutiveViolations = 0
		}
		return false
	}
	b.tokens--
	b.consecutiveViolations = 0
	return true
}

// isBanned returns true when the circuit breaker is active for the bucket.
func (b *tokenBucket) isBanned(now time.Time) bool {
	return now.Before(b.bannedUntil)
}

type keyedLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   int
	buckets map[string]*tokenBucket
	ttl     time.Duration
}

func newKeyedLimiter(rate float64, burst int) *keyedLimiter {
	return &keyedLimiter{
		rate:    rate,
		burst:   burst,
		buckets: make(map[string]*tokenBucket),
		ttl:     time.Hour,
	}
}

func (l *keyedLimiter) allow(key string) bool {
	if l == nil || l.rate <= 0 || l.burst <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[key]
	if b == nil {
		b = newTokenBucket(l.rate, l.burst, now)
		l.buckets[key] = b
	}
	allowed := b.allow(now)
	l.cleanupLocked(now)
	return allowed
}

// banned returns true when the circuit breaker for key is currently active.
// Must be called after allow() returns false to distinguish a ban from ordinary
// throttling.
func (l *keyedLimiter) banned(key string) bool {
	if l == nil {
		return false
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		return false
	}
	return b.isBanned(now)
}

func (l *keyedLimiter) cleanupLocked(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.lastSeen) > l.ttl {
			delete(l.buckets, key)
		}
	}
}
