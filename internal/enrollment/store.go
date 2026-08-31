package enrollment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store persists enrollment tokens to a JSON file in the state directory.
// It is safe for concurrent use.
type Store struct {
	path   string
	mu     sync.Mutex
	tokens map[string]*Token // keyed by token value
}

// NewStore opens or creates a token store at the given path.
func NewStore(stateDir string) (*Store, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("enrollment store: mkdir: %w", err)
	}

	path := filepath.Join(stateDir, "enrollment-tokens.json")
	s := &Store{
		path:   path,
		tokens: make(map[string]*Token),
	}

	// Load existing tokens if the file exists.
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("enrollment store: read %s: %w", path, err)
	}
	if err == nil {
		var tokens []*Token
		if err := json.Unmarshal(data, &tokens); err != nil {
			return nil, fmt.Errorf("enrollment store: parse %s: %w", path, err)
		}
		for _, t := range tokens {
			s.tokens[t.Value] = t
		}
	}

	return s, nil
}

// Put adds or updates a token in the store and persists to disk.
func (s *Store) Put(token *Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tokens[token.Value] = token
	return s.flush()
}

// Get retrieves a token by its full value string. Returns nil if not found.
func (s *Store) Get(value string) *Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[value]
}

// Validate looks up a token and checks it is valid (not used, not expired).
// Returns an error with a specific reason on failure.
func (s *Store) Validate(value string) (*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tok, ok := s.tokens[value]
	if !ok {
		return nil, fmt.Errorf("enrollment: unknown token")
	}
	if tok.Used {
		return nil, fmt.Errorf("enrollment: token already used")
	}
	if tok.IsExpired() {
		return nil, fmt.Errorf("enrollment: token expired")
	}
	return tok, nil
}

// ValidateAndConsume atomically validates a token and marks it as used,
// preventing concurrent enrollment with the same token. The token is
// persisted as used immediately, so even if the enrollment fails
// afterward, the token cannot be reused.
func (s *Store) ValidateAndConsume(value string) (*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tok, ok := s.tokens[value]
	if !ok {
		return nil, fmt.Errorf("enrollment: unknown token")
	}
	if tok.Used {
		return nil, fmt.Errorf("enrollment: token already used")
	}
	if tok.IsExpired() {
		return nil, fmt.Errorf("enrollment: token expired")
	}
	tok.Used = true
	if err := s.flush(); err != nil {
		// Revert in-memory state on flush failure so the token
		// isn't silently consumed without persistence.
		tok.Used = false
		return nil, fmt.Errorf("enrollment: persist token: %w", err)
	}
	return tok, nil
}

// MarkUsed atomically marks a token as used and persists the change.
func (s *Store) MarkUsed(value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tok, ok := s.tokens[value]
	if !ok {
		return fmt.Errorf("enrollment: unknown token")
	}
	if err := tok.MarkUsed(); err != nil {
		return err
	}
	return s.flush()
}

// List returns all tokens (including expired and used ones).
func (s *Store) List() []*Token {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*Token, 0, len(s.tokens))
	for _, t := range s.tokens {
		result = append(result, t)
	}
	return result
}

// flush writes the current token set to disk. Must be called with s.mu held.
func (s *Store) flush() error {
	tokens := make([]*Token, 0, len(s.tokens))
	for _, t := range s.tokens {
		tokens = append(tokens, t)
	}

	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("enrollment store: marshal: %w", err)
	}

	return os.WriteFile(s.path, data, 0o600)
}
