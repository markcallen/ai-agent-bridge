package bridgeclient

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMemoryCursorStore(t *testing.T) {
	store := NewMemoryCursorStore()
	ctx := context.Background()
	const sessionID = "s1"
	const subscriberID = "sub1"

	got, err := store.LoadCursor(ctx, sessionID, subscriberID)
	if err != nil {
		t.Fatalf("LoadCursor empty: %v", err)
	}
	if got != 0 {
		t.Fatalf("LoadCursor empty got=%d want=0", got)
	}
	if err := store.SaveCursor(ctx, sessionID, subscriberID, 42); err != nil {
		t.Fatalf("SaveCursor: %v", err)
	}
	got, err = store.LoadCursor(ctx, sessionID, subscriberID)
	if err != nil {
		t.Fatalf("LoadCursor: %v", err)
	}
	if got != 42 {
		t.Fatalf("LoadCursor got=%d want=42", got)
	}
}

func TestFileCursorStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cursors", "state.json")
	store := NewFileCursorStore(path)
	ctx := context.Background()
	const sessionID = "s2"
	const subscriberID = "sub2"

	if err := store.SaveCursor(ctx, sessionID, subscriberID, 9); err != nil {
		t.Fatalf("SaveCursor: %v", err)
	}
	got, err := store.LoadCursor(ctx, sessionID, subscriberID)
	if err != nil {
		t.Fatalf("LoadCursor: %v", err)
	}
	if got != 9 {
		t.Fatalf("LoadCursor got=%d want=9", got)
	}
}
