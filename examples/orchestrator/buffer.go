package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// cursorForward matches ESC[Nc (cursor-forward N columns) so we can replace
// it with spaces rather than deleting it, preserving word boundaries.
var cursorForward = regexp.MustCompile(`\x1b\[(\d*)C`)

// ansiEscape matches all remaining VT100/ANSI/DEC-private/OSC sequences after
// cursor-forward has already been expanded.
var ansiEscape = regexp.MustCompile(
	`\x1b(?:` +
		`\[[0-9;?!]*[ -/]*[@-~]` + // CSI sequences (including DEC private ?!)
		`|\][^\x07\x1b]*(?:\x07|\x1b\\)` + // OSC sequences (hyperlinks, title)
		`|[@-Z\\-_]` + // two-byte ESC sequences
		`)`,
)

// outputBuffer holds a rolling window of terminal output from a watched session.
// It is safe to write from one goroutine and read from another.
type outputBuffer struct {
	mu           sync.Mutex
	maxSize      int
	data         strings.Builder
	seq          uint64
	lastWriteSeq uint64 // seq of the most recent write, for idle detection
}

func newOutputBuffer(maxSize int) *outputBuffer {
	return &outputBuffer{maxSize: maxSize}
}

// write appends payload and advances the sequence cursor.
func (b *outputBuffer) write(seq uint64, payload []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data.Write(payload)
	if seq > b.seq {
		b.seq = seq
		b.lastWriteSeq = seq
	}
	// Trim to maxSize to avoid unbounded growth.
	if b.data.Len() > b.maxSize {
		s := b.data.String()
		b.data.Reset()
		b.data.WriteString(s[len(s)-b.maxSize:])
	}
}

// snapshot returns the current contents as plain text suitable for LLM analysis.
// Cursor-forward sequences are expanded to spaces so words don't run together;
// all other ANSI/VT100 sequences are stripped.
func (b *outputBuffer) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Replace ESC[Nc with N spaces (N defaults to 1 when absent).
	s := cursorForward.ReplaceAllStringFunc(b.data.String(), func(m string) string {
		sub := cursorForward.FindStringSubmatch(m)
		n := 1
		if sub[1] != "" {
			_, _ = fmt.Sscanf(sub[1], "%d", &n)
		}
		return strings.Repeat(" ", n)
	})
	return ansiEscape.ReplaceAllString(s, "")
}

// idleSeq returns the sequence number at the last write, used to detect
// whether any new output arrived during a watch interval.
func (b *outputBuffer) idleSeq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastWriteSeq
}

// lastSeq returns the highest sequence number seen, used as the AfterSeq
// cursor when re-attaching so we don't replay already-seen output.
func (b *outputBuffer) lastSeq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// reset clears the buffer after a corrective intervention so stale output
// doesn't pollute the next analysis round.
func (b *outputBuffer) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data.Reset()
}
