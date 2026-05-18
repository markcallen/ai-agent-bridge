package config

import (
	"path/filepath"
	"testing"
)

func TestPackagedConfigDoesNotRequireNodeRuntime(t *testing.T) {
	t.Parallel()

	cfg, err := Load(filepath.Join("..", "..", "packaging", "bridge.yaml"))
	if err != nil {
		t.Fatalf("Load(packaging/bridge.yaml): %v", err)
	}

	if RequiresNodeRuntime(cfg) {
		t.Fatal("packaged config unexpectedly requires node runtime")
	}
}
