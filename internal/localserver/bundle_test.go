package localserver

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupFakeClientCreds creates a fake state directory with the PKI files
// that BundleClientCreds expects.
func setupFakeClientCreds(t *testing.T, clientName string) string {
	t.Helper()
	stateDir := t.TempDir()
	certsDir := filepath.Join(stateDir, "certs")
	clientDir := filepath.Join(certsDir, "clients", clientName)
	require.NoError(t, os.MkdirAll(clientDir, 0o700))

	// Create the 4 files BundleClientCreds looks for.
	files := map[string]string{
		filepath.Join(certsDir, "ca-bundle.crt"):    "fake-ca-bundle",
		filepath.Join(clientDir, clientName+".crt"): "fake-client-cert",
		filepath.Join(clientDir, clientName+".key"): "fake-client-key",
		filepath.Join(clientDir, "jwt-signing.key"): "fake-jwt-key",
	}
	for path, content := range files {
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	return stateDir
}

// readTarGz opens a .tar.gz and returns a map of filename → contents.
func readTarGz(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	result := make(map[string]string)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		result[hdr.Name] = string(data)
	}
	return result
}

func TestBundleClientCreds(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		clientName := "test-client"
		stateDir := setupFakeClientCreds(t, clientName)
		outPath := filepath.Join(t.TempDir(), clientName+"-creds.tar.gz")

		err := BundleClientCreds(outPath, stateDir, clientName)
		require.NoError(t, err)

		contents := readTarGz(t, outPath)
		assert.Equal(t, "fake-ca-bundle", contents["ca-bundle.crt"])
		assert.Equal(t, "fake-client-cert", contents["test-client.crt"])
		assert.Equal(t, "fake-client-key", contents["test-client.key"])
		assert.Equal(t, "fake-jwt-key", contents["jwt-signing.key"])
		assert.Len(t, contents, 4)
	})

	t.Run("missing ca-bundle", func(t *testing.T) {
		clientName := "test-client"
		stateDir := setupFakeClientCreds(t, clientName)
		// Remove the CA bundle.
		require.NoError(t, os.Remove(filepath.Join(stateDir, "certs", "ca-bundle.crt")))

		outPath := filepath.Join(t.TempDir(), "out.tar.gz")
		err := BundleClientCreds(outPath, stateDir, clientName)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing credential file")
	})

	t.Run("missing client cert", func(t *testing.T) {
		clientName := "test-client"
		stateDir := setupFakeClientCreds(t, clientName)
		require.NoError(t, os.Remove(filepath.Join(stateDir, "certs", "clients", clientName, clientName+".crt")))

		outPath := filepath.Join(t.TempDir(), "out.tar.gz")
		err := BundleClientCreds(outPath, stateDir, clientName)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing credential file")
	})

	t.Run("invalid output path", func(t *testing.T) {
		clientName := "test-client"
		stateDir := setupFakeClientCreds(t, clientName)

		err := BundleClientCreds("/nonexistent/dir/out.tar.gz", stateDir, clientName)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "create bundle file")
	})
}
