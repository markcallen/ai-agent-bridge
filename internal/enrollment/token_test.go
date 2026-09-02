package enrollment

import (
	"strings"
	"testing"
	"time"
)

func TestGenerate(t *testing.T) {
	tok, err := Generate("agent-node-17", 5*time.Minute, "https://bridge.local", "abc123")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(tok.Value, TokenPrefix) {
		t.Errorf("token should have prefix %q, got %q", TokenPrefix, tok.Value)
	}
	if tok.Identity != "agent-node-17" {
		t.Errorf("Identity = %q, want %q", tok.Identity, "agent-node-17")
	}
	if tok.ServerURL != "https://bridge.local" {
		t.Errorf("ServerURL = %q, want %q", tok.ServerURL, "https://bridge.local")
	}
	if tok.CAFingerprint != "abc123" {
		t.Errorf("CAFingerprint = %q, want %q", tok.CAFingerprint, "abc123")
	}
	if tok.Used {
		t.Error("new token should not be used")
	}
	if tok.IsExpired() {
		t.Error("new token should not be expired")
	}
	if !tok.IsValid() {
		t.Error("new token should be valid")
	}
}

func TestGenerate_RequiresIdentity(t *testing.T) {
	_, err := Generate("", 5*time.Minute, "", "")
	if err == nil {
		t.Error("expected error for empty identity")
	}
}

func TestGenerate_DefaultExpiry(t *testing.T) {
	tok, err := Generate("test", 0, "", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Should use DefaultTokenExpiry (15m).
	expected := time.Now().Add(DefaultTokenExpiry)
	if tok.ExpiresAt.Before(expected.Add(-5 * time.Second)) {
		t.Errorf("ExpiresAt too early: %v", tok.ExpiresAt)
	}
}

func TestToken_Expiry(t *testing.T) {
	tok, _ := Generate("test", 1*time.Millisecond, "", "")
	time.Sleep(5 * time.Millisecond)
	if !tok.IsExpired() {
		t.Error("token should be expired")
	}
	if tok.IsValid() {
		t.Error("expired token should not be valid")
	}
}

func TestToken_MarkUsed(t *testing.T) {
	tok, _ := Generate("test", 5*time.Minute, "", "")
	if err := tok.MarkUsed(); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}
	if !tok.Used {
		t.Error("token should be marked as used")
	}
	if tok.IsValid() {
		t.Error("used token should not be valid")
	}
}

func TestToken_MarkUsed_AlreadyUsed(t *testing.T) {
	tok, _ := Generate("test", 5*time.Minute, "", "")
	_ = tok.MarkUsed()
	if err := tok.MarkUsed(); err == nil {
		t.Error("expected error for double use")
	}
}

func TestToken_MarkUsed_Expired(t *testing.T) {
	tok, _ := Generate("test", 1*time.Millisecond, "", "")
	time.Sleep(5 * time.Millisecond)
	if err := tok.MarkUsed(); err == nil {
		t.Error("expected error for expired token")
	}
}

func TestToken_Redacted(t *testing.T) {
	tok, _ := Generate("test", 5*time.Minute, "", "")
	redacted := tok.Redacted()
	if !strings.HasPrefix(redacted, TokenPrefix) {
		t.Errorf("redacted should have prefix %q, got %q", TokenPrefix, redacted)
	}
	if !strings.HasSuffix(redacted, "***") {
		t.Errorf("redacted should end with ***, got %q", redacted)
	}
	// Should not contain the full token.
	if redacted == tok.Value {
		t.Error("redacted should not equal the full token")
	}
}

func TestToken_Uniqueness(t *testing.T) {
	tok1, _ := Generate("test", 5*time.Minute, "", "")
	tok2, _ := Generate("test", 5*time.Minute, "", "")
	if tok1.Value == tok2.Value {
		t.Error("two tokens should have different values")
	}
}

func TestStore_PutAndGet(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	tok, _ := Generate("test-client", 5*time.Minute, "", "")
	if err := store.Put(tok); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got := store.Get(tok.Value)
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Identity != "test-client" {
		t.Errorf("Identity = %q, want %q", got.Identity, "test-client")
	}
}

func TestStore_Validate(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	tok, _ := Generate("test-client", 5*time.Minute, "", "")
	_ = store.Put(tok)

	got, err := store.Validate(tok.Value)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.Identity != "test-client" {
		t.Errorf("Identity = %q, want %q", got.Identity, "test-client")
	}
}

func TestStore_Validate_Unknown(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	_, err := store.Validate("brg_enroll_nonexistent")
	if err == nil {
		t.Error("expected error for unknown token")
	}
}

func TestStore_Validate_Expired(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	tok, _ := Generate("test", 1*time.Millisecond, "", "")
	_ = store.Put(tok)
	time.Sleep(5 * time.Millisecond)

	_, err := store.Validate(tok.Value)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestStore_MarkUsed_SingleUse(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	tok, _ := Generate("test", 5*time.Minute, "", "")
	_ = store.Put(tok)

	if err := store.MarkUsed(tok.Value); err != nil {
		t.Fatalf("first MarkUsed: %v", err)
	}

	// Second use should fail.
	if err := store.MarkUsed(tok.Value); err == nil {
		t.Error("expected error for reused token")
	}

	// Validate should also fail.
	if _, err := store.Validate(tok.Value); err == nil {
		t.Error("expected error for used token in Validate")
	}
}

func TestStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	store1, _ := NewStore(dir)

	tok, _ := Generate("persist-test", 5*time.Minute, "", "")
	_ = store1.Put(tok)

	// Open a new store from the same directory — should see the token.
	store2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	got := store2.Get(tok.Value)
	if got == nil {
		t.Fatal("token not persisted")
	}
	if got.Identity != "persist-test" {
		t.Errorf("Identity = %q, want %q", got.Identity, "persist-test")
	}
}

func TestStore_List(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	tok1, _ := Generate("client1", 5*time.Minute, "", "")
	tok2, _ := Generate("client2", 5*time.Minute, "", "")
	_ = store.Put(tok1)
	_ = store.Put(tok2)

	list := store.List()
	if len(list) != 2 {
		t.Fatalf("List returned %d tokens, want 2", len(list))
	}
}
