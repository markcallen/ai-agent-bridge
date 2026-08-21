package localserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemotesPath(t *testing.T) {
	got := RemotesPath("/tmp/state")
	want := filepath.Join("/tmp/state", "remotes.yaml")
	if got != want {
		t.Fatalf("RemotesPath = %q, want %q", got, want)
	}
}

func TestLoadRemotesMissingFile(t *testing.T) {
	dir := t.TempDir()
	remotes, err := LoadRemotes(dir)
	if err != nil {
		t.Fatalf("LoadRemotes error = %v", err)
	}
	if len(remotes) != 0 {
		t.Fatalf("expected empty slice, got %d entries", len(remotes))
	}
}

func TestLoadRemotesValidFile(t *testing.T) {
	dir := t.TempDir()
	content := `remotes:
  - name: dev
    host: dev.example.com:9445
  - name: staging
    host: staging.example.com:9445
`
	if err := os.WriteFile(RemotesPath(dir), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	remotes, err := LoadRemotes(dir)
	if err != nil {
		t.Fatalf("LoadRemotes error = %v", err)
	}
	if len(remotes) != 2 {
		t.Fatalf("expected 2 remotes, got %d", len(remotes))
	}
	if remotes[0].Name != "dev" || remotes[0].Host != "dev.example.com:9445" {
		t.Fatalf("unexpected first remote: %+v", remotes[0])
	}
	if remotes[1].Name != "staging" || remotes[1].Host != "staging.example.com:9445" {
		t.Fatalf("unexpected second remote: %+v", remotes[1])
	}
}

func TestAddRemoteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	if err := AddRemote(dir, "dev", "dev.example.com:9445"); err != nil {
		t.Fatalf("AddRemote error = %v", err)
	}
	remotes, err := LoadRemotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 1 {
		t.Fatalf("expected 1 remote, got %d", len(remotes))
	}
	if remotes[0].Name != "dev" || remotes[0].Host != "dev.example.com:9445" {
		t.Fatalf("unexpected remote: %+v", remotes[0])
	}
}

func TestAddRemoteAppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	if err := AddRemote(dir, "dev", "dev.example.com:9445"); err != nil {
		t.Fatal(err)
	}
	if err := AddRemote(dir, "staging", "staging.example.com:9445"); err != nil {
		t.Fatal(err)
	}
	remotes, err := LoadRemotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 2 {
		t.Fatalf("expected 2 remotes, got %d", len(remotes))
	}
}

func TestAddRemoteDuplicateHost(t *testing.T) {
	dir := t.TempDir()
	if err := AddRemote(dir, "dev", "dev.example.com:9445"); err != nil {
		t.Fatal(err)
	}
	if err := AddRemote(dir, "dev", "dev.example.com:9445"); err != nil {
		t.Fatal(err)
	}
	remotes, err := LoadRemotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 1 {
		t.Fatalf("expected 1 remote after duplicate add, got %d", len(remotes))
	}
}

func TestAddRemoteDuplicateHostDifferentName(t *testing.T) {
	dir := t.TempDir()
	if err := AddRemote(dir, "dev", "dev.example.com:9445"); err != nil {
		t.Fatal(err)
	}
	if err := AddRemote(dir, "dev-renamed", "dev.example.com:9445"); err != nil {
		t.Fatal(err)
	}
	remotes, err := LoadRemotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 1 {
		t.Fatalf("expected 1 remote, got %d", len(remotes))
	}
	if remotes[0].Name != "dev" {
		t.Fatalf("expected original name preserved, got %q", remotes[0].Name)
	}
}
