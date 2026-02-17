package pki

import (
	"fmt"
	"os"
)

// BuildBundle concatenates multiple PEM certificate files into a single trust bundle.
func BuildBundle(outPath string, certPaths ...string) error {
	out, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create bundle: %w", err)
	}
	defer out.Close()

	for _, p := range certPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		if _, err := out.Write(data); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
		// Ensure newline between certs
		if len(data) > 0 && data[len(data)-1] != '\n' {
			if _, err := out.Write([]byte{'\n'}); err != nil {
				return fmt.Errorf("write newline: %w", err)
			}
		}
	}
	return nil
}
