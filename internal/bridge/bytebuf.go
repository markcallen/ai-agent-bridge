package bridge

import (
	"sync"
	"time"
)

// ByteBuffer is a bounded ring-like buffer of PTY output chunks with byte-based retention.
type ByteBuffer struct {
	mu       sync.RWMutex
	capacity int
	total    int
	nextSeq  uint64
	chunks   []OutputChunk
}

func NewByteBuffer(capacity int) *ByteBuffer {
	if capacity <= 0 {
		capacity = 8 << 20
	}
	return &ByteBuffer{
		capacity: capacity,
		nextSeq:  1,
	}
}

func (b *ByteBuffer) Append(payload []byte) OutputChunk {
	b.mu.Lock()
	defer b.mu.Unlock()

	copied := append([]byte(nil), payload...)
	chunk := OutputChunk{
		Seq:       b.nextSeq,
		Timestamp: nowUTC(),
		Payload:   copied,
	}
	b.nextSeq++
	b.chunks = append(b.chunks, chunk)
	b.total += len(copied)
	for b.total > b.capacity && len(b.chunks) > 0 {
		b.total -= len(b.chunks[0].Payload)
		b.chunks = b.chunks[1:]
	}
	return chunk
}

func (b *ByteBuffer) After(afterSeq uint64) []OutputChunk {
	b.mu.RLock()
	defer b.mu.RUnlock()

	out := make([]OutputChunk, 0, len(b.chunks))
	for _, chunk := range b.chunks {
		if chunk.Seq <= afterSeq {
			continue
		}
		out = append(out, OutputChunk{
			Seq:       chunk.Seq,
			Timestamp: chunk.Timestamp,
			Payload:   append([]byte(nil), chunk.Payload...),
		})
	}
	return out
}

func (b *ByteBuffer) OldestSeq() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.chunks) == 0 {
		return 0
	}
	return b.chunks[0].Seq
}

func (b *ByteBuffer) LastSeq() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.chunks) == 0 {
		return 0
	}
	return b.chunks[len(b.chunks)-1].Seq
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
