package bridgelib

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestNewAppliesDefaults(t *testing.T) {
	b, err := New(Config{
		Providers: []ProviderConfig{{
			ID:     "cat",
			Binary: "/bin/cat",
		}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(b.supervisor.Close)

	if got := len(b.registry.List()); got != 1 {
		t.Fatalf("registry size=%d want 1", got)
	}
}

func TestBridgeLifecycle(t *testing.T) {
	repo := t.TempDir()
	b, err := New(Config{
		Providers: []ProviderConfig{{
			ID:     "cat",
			Binary: "/bin/cat",
		}},
		AllowedPaths: []string{repo},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(b.supervisor.Close)

	info, err := b.StartSession(context.Background(), "project-a", "session-a", repo, "cat", nil)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	state, err := b.AttachSession("session-a", "client-a", 0)
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	if _, err := b.WriteInput("session-a", "client-a", []byte("hello\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	select {
	case chunk := <-state.Live:
		if !bytes.Contains(chunk.Payload, []byte("hello")) {
			t.Fatalf("payload=%q does not contain hello", string(chunk.Payload))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for live output")
	}

	if err := b.ResizeSession("session-a", "client-a", 100, 40); err != nil {
		t.Fatalf("ResizeSession: %v", err)
	}
	got, err := b.Get("session-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Cols != 100 || got.Rows != 40 || info.Provider != "cat" {
		t.Fatalf("session info=%+v start=%+v", got, info)
	}

	if len(b.List("project-a")) != 1 {
		t.Fatal("List(project-a) was empty")
	}
	if err := b.supervisor.Detach("session-a", "client-a"); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if err := b.Stop("session-a", true); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
