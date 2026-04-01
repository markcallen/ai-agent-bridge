package bridge

import "testing"

func TestByteBufferEvictsOldestByBytes(t *testing.T) {
	buf := NewByteBuffer(5)
	first := buf.Append([]byte("abc"))
	second := buf.Append([]byte("de"))
	third := buf.Append([]byte("fg"))

	if first.Seq != 1 || second.Seq != 2 || third.Seq != 3 {
		t.Fatalf("unexpected seqs: %d %d %d", first.Seq, second.Seq, third.Seq)
	}
	if got := buf.OldestSeq(); got != 2 {
		t.Fatalf("OldestSeq=%d want=2", got)
	}
	items := buf.After(0)
	if len(items) != 2 {
		t.Fatalf("After(0) len=%d want=2", len(items))
	}
	if string(items[0].Payload) != "de" || string(items[1].Payload) != "fg" {
		t.Fatalf("unexpected payloads: %q %q", items[0].Payload, items[1].Payload)
	}
}
