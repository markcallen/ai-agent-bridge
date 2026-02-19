package bridgeclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// CursorStore persists last acknowledged sequence numbers by session/subscriber.
type CursorStore interface {
	LoadCursor(ctx context.Context, sessionID, subscriberID string) (uint64, error)
	SaveCursor(ctx context.Context, sessionID, subscriberID string, seq uint64) error
}

// MemoryCursorStore stores cursors in-memory.
type MemoryCursorStore struct {
	mu   sync.RWMutex
	data map[string]uint64
}

// NewMemoryCursorStore creates an in-memory cursor store.
func NewMemoryCursorStore() *MemoryCursorStore {
	return &MemoryCursorStore{
		data: make(map[string]uint64),
	}
}

func (s *MemoryCursorStore) LoadCursor(ctx context.Context, sessionID, subscriberID string) (uint64, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[cursorKey(sessionID, subscriberID)], nil
}

func (s *MemoryCursorStore) SaveCursor(ctx context.Context, sessionID, subscriberID string, seq uint64) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[cursorKey(sessionID, subscriberID)] = seq
	return nil
}

// FileCursorStore stores cursors in a JSON file for cross-process resume.
type FileCursorStore struct {
	mu   sync.Mutex
	path string
}

// NewFileCursorStore creates a file-backed cursor store.
func NewFileCursorStore(path string) *FileCursorStore {
	return &FileCursorStore{path: path}
}

func (s *FileCursorStore) LoadCursor(ctx context.Context, sessionID, subscriberID string) (uint64, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("load cursor file: %w", err)
	}
	all := map[string]uint64{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &all); err != nil {
			return 0, fmt.Errorf("parse cursor file: %w", err)
		}
	}
	return all[cursorKey(sessionID, subscriberID)], nil
}

func (s *FileCursorStore) SaveCursor(ctx context.Context, sessionID, subscriberID string, seq uint64) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	all := map[string]uint64{}
	data, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read cursor file: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &all); err != nil {
			return fmt.Errorf("parse cursor file: %w", err)
		}
	}
	all[cursorKey(sessionID, subscriberID)] = seq
	encoded, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cursor file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir cursor dir: %w", err)
	}
	if err := os.WriteFile(s.path, encoded, 0o644); err != nil {
		return fmt.Errorf("write cursor file: %w", err)
	}
	return nil
}

func cursorKey(sessionID, subscriberID string) string {
	return sessionID + ":" + subscriberID
}
