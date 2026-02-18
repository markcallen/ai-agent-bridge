package bridge

import (
	"testing"
	"time"
)

func newTestBuffer(cap int) *EventBuffer {
	return NewEventBuffer(cap)
}

func TestAttachDetach(t *testing.T) {
	buf := newTestBuffer(100)
	mgr := NewSubscriberManager(buf, DefaultSubscriberConfig())

	// Append some events first.
	buf.Append(Event{Text: "e1"})
	buf.Append(Event{Text: "e2"})

	result, err := mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(result.Replay) != 2 {
		t.Fatalf("replay len = %d, want 2", len(result.Replay))
	}
	if result.Live == nil {
		t.Fatal("live channel is nil")
	}
	if result.Overflow {
		t.Error("unexpected overflow")
	}

	// Detach preserves cursor.
	mgr.Detach("sub1", result.Live)
	if mgr.SubscriberCount() != 1 {
		t.Errorf("subscriber count after detach = %d, want 1", mgr.SubscriberCount())
	}
}

func TestReconnectReplay(t *testing.T) {
	buf := newTestBuffer(100)
	mgr := NewSubscriberManager(buf, DefaultSubscriberConfig())

	buf.Append(Event{Text: "e1"})
	buf.Append(Event{Text: "e2"})

	// First connection: ack seq 1.
	result, err := mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	mgr.Ack("sub1", 1)
	mgr.Detach("sub1", result.Live)

	// More events produced while disconnected.
	buf.Append(Event{Text: "e3"})

	// Reconnect: should replay from ack_seq=1, getting e2 and e3.
	result, err = mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Re-attach: %v", err)
	}
	defer mgr.Detach("sub1", result.Live)

	if len(result.Replay) != 2 {
		t.Fatalf("replay len = %d, want 2 (e2, e3)", len(result.Replay))
	}
	if result.Replay[0].Seq != 2 {
		t.Errorf("first replay seq = %d, want 2", result.Replay[0].Seq)
	}
	if result.Replay[1].Seq != 3 {
		t.Errorf("second replay seq = %d, want 3", result.Replay[1].Seq)
	}
}

func TestAckProgression(t *testing.T) {
	buf := newTestBuffer(100)
	mgr := NewSubscriberManager(buf, DefaultSubscriberConfig())

	for i := 0; i < 5; i++ {
		buf.Append(Event{Text: "e"})
	}

	result, err := mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// Ack through seq 3.
	mgr.Ack("sub1", 3)
	mgr.Detach("sub1", result.Live)

	// Re-attach: should replay from seq 3 onward (4 and 5).
	result, err = mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Re-attach: %v", err)
	}
	defer mgr.Detach("sub1", result.Live)

	if len(result.Replay) != 2 {
		t.Fatalf("replay len = %d, want 2", len(result.Replay))
	}
	if result.Replay[0].Seq != 4 {
		t.Errorf("first seq = %d, want 4", result.Replay[0].Seq)
	}
}

func TestBufferOverflow(t *testing.T) {
	buf := newTestBuffer(3)
	mgr := NewSubscriberManager(buf, DefaultSubscriberConfig())

	// Subscriber attaches and acks seq 1.
	buf.Append(Event{Text: "e1"})
	result, err := mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	mgr.Ack("sub1", 1)
	mgr.Detach("sub1", result.Live)

	// Fill buffer past capacity: events 2,3,4,5 (buffer holds 3,4,5).
	buf.Append(Event{Text: "e2"})
	buf.Append(Event{Text: "e3"})
	buf.Append(Event{Text: "e4"})
	buf.Append(Event{Text: "e5"})

	// Reconnect: ack_seq=1 is behind oldest=3, so overflow.
	result, err = mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Re-attach: %v", err)
	}
	defer mgr.Detach("sub1", result.Live)

	if !result.Overflow {
		t.Error("expected overflow=true")
	}
}

func TestMultiSubscriberFanout(t *testing.T) {
	buf := newTestBuffer(100)
	mgr := NewSubscriberManager(buf, DefaultSubscriberConfig())

	for i := 0; i < 5; i++ {
		buf.Append(Event{Text: "e"})
	}

	// sub1 acked 2, sub2 acked 4.
	r1, err := mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Attach sub1: %v", err)
	}
	mgr.Ack("sub1", 2)
	mgr.Detach("sub1", r1.Live)

	r2, err := mgr.Attach("sub2", 0)
	if err != nil {
		t.Fatalf("Attach sub2: %v", err)
	}
	mgr.Ack("sub2", 4)
	mgr.Detach("sub2", r2.Live)

	// Re-attach both: different replay sets.
	r1, err = mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Re-attach sub1: %v", err)
	}
	defer mgr.Detach("sub1", r1.Live)

	r2, err = mgr.Attach("sub2", 0)
	if err != nil {
		t.Fatalf("Re-attach sub2: %v", err)
	}
	defer mgr.Detach("sub2", r2.Live)

	if len(r1.Replay) != 3 {
		t.Errorf("sub1 replay len = %d, want 3", len(r1.Replay))
	}
	if len(r2.Replay) != 1 {
		t.Errorf("sub2 replay len = %d, want 1", len(r2.Replay))
	}
}

func TestMaxSubscribers(t *testing.T) {
	buf := newTestBuffer(100)
	cfg := DefaultSubscriberConfig()
	cfg.MaxSubscribersPerSession = 2
	mgr := NewSubscriberManager(buf, cfg)

	r1, err := mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Attach sub1: %v", err)
	}
	defer mgr.Detach("sub1", r1.Live)

	r2, err := mgr.Attach("sub2", 0)
	if err != nil {
		t.Fatalf("Attach sub2: %v", err)
	}
	defer mgr.Detach("sub2", r2.Live)

	_, err = mgr.Attach("sub3", 0)
	if err == nil {
		t.Error("expected error for exceeding max subscribers")
	}
}

func TestCleanupExpired(t *testing.T) {
	buf := newTestBuffer(100)
	cfg := DefaultSubscriberConfig()
	cfg.SubscriberTTL = 10 * time.Millisecond
	mgr := NewSubscriberManager(buf, cfg)

	r, err := mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	mgr.Detach("sub1", r.Live)

	// Wait for TTL to expire.
	time.Sleep(20 * time.Millisecond)
	mgr.CleanupExpired()

	if mgr.SubscriberCount() != 0 {
		t.Errorf("subscriber count = %d, want 0 after cleanup", mgr.SubscriberCount())
	}

	// Fresh attach should work (starts from scratch).
	r, err = mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Re-attach after cleanup: %v", err)
	}
	defer mgr.Detach("sub1", r.Live)
}

func TestReplayToLiveNoGap(t *testing.T) {
	buf := newTestBuffer(100)
	mgr := NewSubscriberManager(buf, DefaultSubscriberConfig())

	// Pre-fill some events.
	buf.Append(Event{Text: "e1"})
	buf.Append(Event{Text: "e2"})

	result, err := mgr.Attach("sub1", 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer mgr.Detach("sub1", result.Live)

	// Replay should have e1, e2.
	if len(result.Replay) != 2 {
		t.Fatalf("replay len = %d, want 2", len(result.Replay))
	}

	// Now append e3 after attach but before consuming live.
	buf.Append(Event{Text: "e3"})

	// e3 should appear on the live channel (since Subscribe happened before After).
	select {
	case se := <-result.Live:
		if se.Seq != 3 {
			t.Errorf("live event seq = %d, want 3", se.Seq)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for live event")
	}

	// The live channel may also have received e1 and e2 (they were appended before
	// subscribe but the channel was created before After). We just need to ensure
	// e3 is eventually received -- which we already verified above.
}
