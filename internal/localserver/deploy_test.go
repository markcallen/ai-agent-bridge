package localserver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeployBundle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake scp script not supported on Windows")
	}

	t.Run("scp not found", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		err := DeployBundle("/tmp/bundle.tar.gz", "host:~/creds/", testLogger())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scp not found")
	})

	t.Run("scp receives correct arguments", func(t *testing.T) {
		dir := t.TempDir()
		argsFile := filepath.Join(dir, "scp-args")

		// Create a fake scp that records its arguments.
		script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\nexit 0\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "scp"), []byte(script), 0o755))
		t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

		bundlePath := filepath.Join(dir, "test.tar.gz")
		require.NoError(t, os.WriteFile(bundlePath, []byte("fake"), 0o644))

		err := DeployBundle(bundlePath, "do-dev2:~/bridge-creds/", testLogger())
		require.NoError(t, err)

		args, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		assert.Contains(t, string(args), bundlePath)
		assert.Contains(t, string(args), "do-dev2:~/bridge-creds/")
	})

	t.Run("scp failure propagates", func(t *testing.T) {
		dir := t.TempDir()
		script := "#!/bin/sh\nexit 1\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "scp"), []byte(script), 0o755))
		t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

		err := DeployBundle("/tmp/bundle.tar.gz", "host:~/creds/", testLogger())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scp failed")
	})
}
