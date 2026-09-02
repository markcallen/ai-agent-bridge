package localserver

import (
	"log/slog"

	"github.com/orchael/bridgectl/internal/certprovider"
	"github.com/orchael/bridgectl/internal/config"
)

// CertProviderFromConfig constructs a CertificateProvider based on the
// security configuration. This bridges the new v1.1 SecurityConfig model
// to the certprovider package.
//
// For the "auto" and "stepca" providers, the returned provider delegates
// to the same underlying functions as the existing EnsurePKI and
// RenewServerCertMaterial code paths. For "filesystem", it reads
// pre-existing certificate material from disk.
//
// Note: EnsurePKI remains the authoritative function for setting up the
// full PKI state directory (CA, bundles, JWT keypairs, etc.). This
// function provides a provider handle for operations that only need
// the CertificateProvider interface (enrollment, renewal, root discovery).
func CertProviderFromConfig(secCfg config.SecurityConfig, stateDir string, logger *slog.Logger) (certprovider.CertificateProvider, error) {
	providerName := secCfg.Certificates.Provider
	if providerName == "" {
		providerName = "auto"
	}

	switch providerName {
	case "auto":
		return certprovider.NewAutoProvider(CertsDir(stateDir), "bridgectl-ca")

	case "filesystem":
		fs := secCfg.Certificates.Filesystem
		return certprovider.NewFilesystemProvider(certprovider.FilesystemConfig{
			CACertPath: fs.CA,
			CertPath:   firstNonEmpty(fs.Certificate, fs.ServerCert),
			KeyPath:    firstNonEmpty(fs.PrivateKey, fs.ServerKey),
		})

	case "stepca":
		sc := secCfg.Certificates.StepCA
		p, err := certprovider.NewStepCAProvider(certprovider.StepCAConfig{
			URL:                     sc.URL,
			RootPath:                sc.Root,
			Provisioner:             sc.Provisioner,
			ProvisionerPasswordFile: sc.ProvisionerPasswordFile,
		}, logger)
		if err != nil {
			return nil, err
		}
		// Wire the existing Step CA functions into the provider so it
		// uses the same tested code paths as EnsurePKI/RenewServerCertMaterial.
		p.RequestJWK = stepCARequestJWKAdapter
		p.RequestACME = stepCARequestACMEAdapter
		p.RenewMTLS = stepCARenewMTLSAdapter
		return p, nil

	default:
		return certprovider.New(providerName, certprovider.ProviderConfig{})
	}
}

// StepCAConfigFromProvider extracts a legacy StepCAConfig from a SecurityConfig.
// This is used during the transition period while EnsurePKI still needs
// the old config format.
func StepCAConfigFromProvider(secCfg config.SecurityConfig) *StepCAConfig {
	if secCfg.Certificates.Provider != "stepca" {
		return nil
	}
	sc := secCfg.Certificates.StepCA
	if sc.URL == "" {
		return nil
	}
	return &StepCAConfig{
		URL:                     sc.URL,
		RootPath:                sc.Root,
		Provisioner:             sc.Provisioner,
		ProvisionerPasswordFile: sc.ProvisionerPasswordFile,
	}
}

// Adapters that bridge certprovider.StepCAConfig to localserver.StepCAConfig.
func stepCARequestJWKAdapter(cfg *certprovider.StepCAConfig, sans []string, certPath, keyPath string, logger *slog.Logger) error {
	return requestCertJWKFn(convertStepCACfg(cfg), sans, certPath, keyPath, logger)
}

func stepCARequestACMEAdapter(cfg *certprovider.StepCAConfig, sans []string, certPath, keyPath string, logger *slog.Logger) error {
	return requestCertACMEFn(convertStepCACfg(cfg), sans, certPath, keyPath, logger)
}

func stepCARenewMTLSAdapter(cfg *certprovider.StepCAConfig, certPath, keyPath string, logger *slog.Logger) error {
	return renewCertMTLSFn(convertStepCACfg(cfg), certPath, keyPath, logger)
}

func convertStepCACfg(src *certprovider.StepCAConfig) *StepCAConfig {
	return &StepCAConfig{
		URL:                     src.URL,
		RootPath:                src.RootPath,
		Provisioner:             src.Provisioner,
		ProvisionerPasswordFile: src.ProvisionerPasswordFile,
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
