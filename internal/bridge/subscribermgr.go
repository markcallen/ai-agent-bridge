package bridge

import (
	"fmt"
	"sync"
	"time"
)

// SubscriberConfig controls per-session subscriber limits and TTL.
type SubscriberConfig struct {
	MaxSubscribersPerSession int
	SubscriberTTL            time.Duration
}

// DefaultSubscriberConfig returns sensible defaults.
func DefaultSubscriberConfig() SubscriberConfig {
	return SubscriberConfig{
		MaxSubscribersPerSession: 10,
		SubscriberTTL:            30 * time.Minute,
	}
}

// ErrSubscriberLimitReached is returned when a session has too many subscribers.
var ErrSubscriberLimitReached = fmt.Errorf("subscriber limit reached")

type subscriberState struct {
	subscriberID string
	ackSeq       uint64
	lastSeen     time.Time
}

// SubscriberManager tracks per-subscriber cursors on top of an EventBuffer.
type SubscriberManager struct {
	mu          sync.Mutex
	buf         *EventBuffer
	config      SubscriberConfig
	subscribers map[string]*subscriberState
}

// NewSubscriberManager creates a manager wrapping the given EventBuffer.
func NewSubscriberManager(buf *EventBuffer, cfg SubscriberConfig) *SubscriberManager {
	return &SubscriberManager{
		buf:         buf,
		config:      cfg,
		subscribers: make(map[string]*subscriberState),
	}
}

// AttachResult holds the return values of Attach.
type AttachResult struct {
	Replay   []SequencedEvent
	Live     chan SequencedEvent
	Overflow bool
}

// Attach connects a subscriber, returning replay events and a live channel.
// Subscribe first (for live), then replay (for history), so no events are missed.
// If the subscriber's ack_seq is behind the buffer's oldest event, overflow is true.
func (m *SubscriberManager) Attach(subscriberID string, afterSeq uint64) (*AttachResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, exists := m.subscribers[subscriberID]
	if exists {
		// Resuming: use the stored ack cursor if it's ahead of the requested afterSeq
		if sub.ackSeq > afterSeq {
			afterSeq = sub.ackSeq
		}
		sub.lastSeen = time.Now()
	} else {
		// New subscriber: check limit
		if len(m.subscribers) >= m.config.MaxSubscribersPerSession {
			return nil, fmt.Errorf("%w: max %d", ErrSubscriberLimitReached, m.config.MaxSubscribersPerSession)
		}
		sub = &subscriberState{
			subscriberID: subscriberID,
			ackSeq:       afterSeq,
			lastSeen:     time.Now(),
		}
		m.subscribers[subscriberID] = sub
	}

	// Subscribe to live events first to close the replay-to-live gap.
	live := m.buf.Subscribe()

	// Now get the replay.
	replay := m.buf.After(afterSeq)

	// Detect overflow: if the subscriber's afterSeq is behind the oldest buffered event.
	overflow := false
	oldest := m.buf.OldestSeq()
	if oldest > 0 && afterSeq > 0 && afterSeq < oldest-1 {
		overflow = true
	}

	return &AttachResult{
		Replay:   replay,
		Live:     live,
		Overflow: overflow,
	}, nil
}

// Detach removes the live channel but preserves the subscriber's cursor for reconnect.
func (m *SubscriberManager) Detach(subscriberID string, ch chan SequencedEvent) {
	m.buf.Unsubscribe(ch)
	m.mu.Lock()
	if sub, ok := m.subscribers[subscriberID]; ok {
		sub.lastSeen = time.Now()
	}
	m.mu.Unlock()
}

// Ack advances the subscriber's acknowledged sequence number.
func (m *SubscriberManager) Ack(subscriberID string, seq uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sub, ok := m.subscribers[subscriberID]; ok {
		if seq > sub.ackSeq {
			sub.ackSeq = seq
			sub.lastSeen = time.Now()
		}
	}
}

// CleanupExpired removes subscribers that haven't been seen since TTL.
func (m *SubscriberManager) CleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-m.config.SubscriberTTL)
	for id, sub := range m.subscribers {
		if sub.lastSeen.Before(cutoff) {
			delete(m.subscribers, id)
		}
	}
}

// SubscriberCount returns the number of tracked subscribers (for testing).
func (m *SubscriberManager) SubscriberCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.subscribers)
}
