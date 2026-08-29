package localserver

import (
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/orchael/bridgectl/internal/auth"
	"github.com/orchael/bridgectl/internal/pki"
)

// ConfiguredJWTClient declares a client JWT public key that should be loaded
// during secure startup when present.
type ConfiguredJWTClient struct {
	Issuer   string
	KeyPath  string
	Required bool
}

func loadConfiguredJWTClients(verifier *auth.JWTVerifier, stateDir string, logger *slog.Logger, clients []ConfiguredJWTClient) error {
	if verifier == nil || len(clients) == 0 {
		return nil
	}
	for _, client := range clients {
		keyPath := client.KeyPath
		if keyPath == "" {
			keyPath = filepath.Join(CertsDir(stateDir), "jwt-clients", client.Issuer+".pub")
		}
		pub, err := pki.LoadEd25519PublicKey(keyPath)
		if err != nil {
			if !client.Required {
				if logger != nil {
					logger.Warn("configured Step CA client JWT key not available", "issuer", client.Issuer, "path", keyPath, "error", err)
				}
				continue
			}
			return fmt.Errorf("load configured Step CA client JWT key for issuer %q: %w", client.Issuer, err)
		}
		verifier.AddKey(client.Issuer, ed25519.PublicKey(pub))
		if logger != nil {
			logger.Info("loaded configured Step CA client JWT key", "issuer", client.Issuer, "path", keyPath)
		}
	}
	return nil
}
