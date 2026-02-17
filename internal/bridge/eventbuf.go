package bridge

import (
	"sync"
)

// SequencedEvent is an Event with a monotonic sequence number assigned by the buffer.
type SequencedEvent struct {
	Seq uint64
	Event
}

// EventBuffer is a bounded ring buffer for session events with subscribe/replay support.
type EventBuffer struct {
	mu       sync.RWMutex
	buf      []SequencedEvent
	capacity int
	nextSeq  uint64
	head     int // oldest element index
	count    int // number of elements in buffer

	subMu sync.RWMutex
	subs  map[chan SequencedEvent]struct{}
}

// NewEventBuffer creates a new ring buffer with the given capacity.
func NewEventBuffer(capacity int) *EventBuffer {
	if capacity < 1 {
		capacity = 1000
	}
	return &EventBuffer{
		buf:      make([]SequencedEvent, capacity),
		capacity: capacity,
		nextSeq:  1,
		subs:     make(map[chan SequencedEvent]struct{}),
	}
}

// Append adds an event to the buffer, assigns a sequence number, and notifies subscribers.
// Returns the assigned sequence number.
func (b *EventBuffer) Append(e Event) uint64 {
	b.mu.Lock()

	seq := b.nextSeq
	b.nextSeq++

	se := SequencedEvent{Seq: seq, Event: e}

	if b.count < b.capacity {
		idx := (b.head + b.count) % b.capacity
		b.buf[idx] = se
		b.count++
	} else {
		// Overwrite oldest
		b.buf[b.head] = se
		b.head = (b.head + 1) % b.capacity
	}

	b.mu.Unlock()

	// Notify subscribers (non-blocking)
	b.subMu.RLock()
	for ch := range b.subs {
		select {
		case ch <- se:
		default:
			// Subscriber too slow, drop
		}
	}
	b.subMu.RUnlock()

	return seq
}

// After returns all buffered events with sequence number > afterSeq.
func (b *EventBuffer) After(afterSeq uint64) []SequencedEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []SequencedEvent
	for i := 0; i < b.count; i++ {
		idx := (b.head + i) % b.capacity
		if b.buf[idx].Seq > afterSeq {
			result = append(result, b.buf[idx])
		}
	}
	return result
}

// Subscribe returns a channel that receives new events as they are appended.
// Call Unsubscribe to release the channel.
func (b *EventBuffer) Subscribe() chan SequencedEvent {
	ch := make(chan SequencedEvent, 64)
	b.subMu.Lock()
	b.subs[ch] = struct{}{}
	b.subMu.Unlock()
	return ch
}

// Unsubscribe removes a subscription channel and closes it.
func (b *EventBuffer) Unsubscribe(ch chan SequencedEvent) {
	b.subMu.Lock()
	delete(b.subs, ch)
	b.subMu.Unlock()
	// Drain and close
	for {
		select {
		case <-ch:
		default:
			close(ch)
			return
		}
	}
}

// Len returns the number of events currently in the buffer.
func (b *EventBuffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.count
}

// LastSeq returns the sequence number of the most recently appended event.
func (b *EventBuffer) LastSeq() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.nextSeq == 1 {
		return 0
	}
	return b.nextSeq - 1
}
