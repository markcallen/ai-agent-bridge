package localserver

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BundleClientCreds creates a .tar.gz archive containing the 4 files needed
// by a remote client: ca-bundle.crt, <name>.crt, <name>.key, jwt-signing.key.
// Files are stored flat (no directory prefix) so they extract into the current
// directory.
func BundleClientCreds(outPath, stateDir, clientName string) error {
	mat := LoadPKIMaterial(stateDir)
	clientDir := filepath.Join(CertsDir(stateDir), "clients", clientName)

	// The 4 files a remote client needs, mapped to their archive names.
	files := []struct {
		path     string
		archName string
	}{
		{mat.CABundlePath, "ca-bundle.crt"},
		{filepath.Join(clientDir, clientName+".crt"), clientName + ".crt"},
		{filepath.Join(clientDir, clientName+".key"), clientName + ".key"},
		{filepath.Join(clientDir, "jwt-signing.key"), "jwt-signing.key"},
	}

	// Verify all files exist before creating the archive.
	for _, f := range files {
		if _, err := os.Stat(f.path); err != nil {
			return fmt.Errorf("missing credential file %s: %w", f.path, err)
		}
	}

	outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create bundle file: %w", err)
	}
	defer func() { _ = outFile.Close() }()

	gw := gzip.NewWriter(outFile)
	tw := tar.NewWriter(gw)

	for _, f := range files {
		if err := addFileToTar(tw, f.path, f.archName); err != nil {
			_ = tw.Close()
			_ = gw.Close()
			return fmt.Errorf("add %s to bundle: %w", f.archName, err)
		}
	}

	if err := tw.Close(); err != nil {
		_ = gw.Close()
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("close gzip writer: %w", err)
	}
	return outFile.Close()
}

// addFileToTar adds a single file to the tar archive with the given name.
func addFileToTar(tw *tar.Writer, filePath, archName string) error {
	fi, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	header := &tar.Header{
		Name: archName,
		Size: fi.Size(),
		Mode: int64(fi.Mode()),
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = io.Copy(tw, f)
	return err
}
