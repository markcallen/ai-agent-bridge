package bridge

import (
	"testing"
	"time"
)

func TestEventBufferAppendAndAfter(t *testing.T) {
	buf := NewEventBuffer(5)

	for i := 0; i < 5; i++ {
		buf.Append(Event{Text: "event"})
	}

	if buf.Len() != 5 {
		t.Errorf("Len = %d, want 5", buf.Len())
	}

	events := buf.After(0)
	if len(events) != 5 {
		t.Fatalf("After(0) returned %d events, want 5", len(events))
	}

	events = buf.After(3)
	if len(events) != 2 {
		t.Errorf("After(3) returned %d events, want 2", len(events))
	}
	if events[0].Seq != 4 {
		t.Errorf("first event seq = %d, want 4", events[0].Seq)
	}
}

func TestEventBufferOverflow(t *testing.T) {
	buf := NewEventBuffer(3)

	for i := 1; i <= 5; i++ {
		buf.Append(Event{Text: "event"})
	}

	if buf.Len() != 3 {
		t.Errorf("Len = %d, want 3", buf.Len())
	}

	events := buf.After(0)
	if len(events) != 3 {
		t.Fatalf("After(0) returned %d events, want 3", len(events))
	}
	// Oldest events (1,2) should be dropped, remaining: 3,4,5
	if events[0].Seq != 3 {
		t.Errorf("oldest seq = %d, want 3", events[0].Seq)
	}
	if events[2].Seq != 5 {
		t.Errorf("newest seq = %d, want 5", events[2].Seq)
	}
}

func TestEventBufferSubscribe(t *testing.T) {
	buf := NewEventBuffer(10)

	ch := buf.Subscribe()
	defer buf.Unsubscribe(ch)

	buf.Append(Event{Text: "hello"})

	select {
	case e := <-ch:
		if e.Text != "hello" {
			t.Errorf("got text %q, want %q", e.Text, "hello")
		}
		if e.Seq != 1 {
			t.Errorf("got seq %d, want 1", e.Seq)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for event")
	}
}

func TestEventBufferLastSeq(t *testing.T) {
	buf := NewEventBuffer(10)

	if buf.LastSeq() != 0 {
		t.Errorf("LastSeq on empty = %d, want 0", buf.LastSeq())
	}

	buf.Append(Event{Text: "a"})
	buf.Append(Event{Text: "b"})

	if buf.LastSeq() != 2 {
		t.Errorf("LastSeq = %d, want 2", buf.LastSeq())
	}
}
