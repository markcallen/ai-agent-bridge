package server

import (
	"sync"
	"time"
)

type tokenBucket struct {
	rate     float64
	burst    float64
	tokens   float64
	last     time.Time
	lastSeen time.Time
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

func (b *tokenBucket) allow(now time.Time) bool {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
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

func (l *keyedLimiter) cleanupLocked(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.lastSeen) > l.ttl {
			delete(l.buckets, key)
		}
	}
}
