package certprovider

import (
	"fmt"
	"log/slog"
)

// ProviderConfig holds configuration for constructing a CertificateProvider.
// Only the fields relevant to the selected provider name need to be set.
type ProviderConfig struct {
	// Filesystem provider configuration.
	Filesystem *FilesystemConfig

	// Auto provider configuration.
	AutoCertsDir string
	AutoCAName   string

	// Step CA provider configuration.
	StepCA *StepCAConfig
	Logger *slog.Logger
}

// New constructs a CertificateProvider by name. Supported names are
// "filesystem", "auto", and "stepca".
func New(name string, cfg ProviderConfig) (CertificateProvider, error) {
	switch name {
	case "filesystem":
		if cfg.Filesystem == nil {
			return nil, fmt.Errorf("filesystem provider config is required")
		}
		return NewFilesystemProvider(*cfg.Filesystem)

	case "auto":
		return NewAutoProvider(cfg.AutoCertsDir, cfg.AutoCAName)

	case "stepca":
		if cfg.StepCA == nil {
			return nil, fmt.Errorf("stepca provider config is required")
		}
		return NewStepCAProvider(*cfg.StepCA, cfg.Logger)

	default:
		return nil, fmt.Errorf("unknown certificate provider: %q", name)
	}
}
