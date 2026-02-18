package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level bridge daemon configuration.
type Config struct {
	Server       ServerConfig              `yaml:"server"`
	TLS          TLSConfig                 `yaml:"tls"`
	Auth         AuthConfig                `yaml:"auth"`
	Sessions     SessionsConfig            `yaml:"sessions"`
	Input        InputConfig               `yaml:"input"`
	Providers    map[string]ProviderConfig `yaml:"providers"`
	AllowedPaths []string                  `yaml:"allowed_paths"`
	Logging      LoggingConfig             `yaml:"logging"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
}

type TLSConfig struct {
	CABundle string `yaml:"ca_bundle"`
	Cert     string `yaml:"cert"`
	Key      string `yaml:"key"`
}

type AuthConfig struct {
	JWTPublicKeys []JWTKeyConfig `yaml:"jwt_public_keys"`
	JWTAudience   string         `yaml:"jwt_audience"`
	JWTMaxTTL     string         `yaml:"jwt_max_ttl"`
}

type JWTKeyConfig struct {
	Issuer  string `yaml:"issuer"`
	KeyPath string `yaml:"key_path"`
}

type SessionsConfig struct {
	MaxPerProject   int    `yaml:"max_per_project"`
	MaxGlobal       int    `yaml:"max_global"`
	IdleTimeout     string `yaml:"idle_timeout"`
	StopGracePeriod string `yaml:"stop_grace_period"`
	EventBufferSize int    `yaml:"event_buffer_size"`
}

type InputConfig struct {
	MaxSizeBytes int `yaml:"max_size_bytes"`
}

type ProviderConfig struct {
	Binary         string   `yaml:"binary"`
	Args           []string `yaml:"args"`
	StartupTimeout string   `yaml:"startup_timeout"`
	PTY            bool     `yaml:"pty"`
}

type LoggingConfig struct {
	Level          string   `yaml:"level"`
	Format         string   `yaml:"format"`
	RedactPatterns []string `yaml:"redact_patterns"`
}

// Load reads and parses a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(cfg)
	return cfg, nil
}

// ParseDuration is a helper that parses a duration string with a fallback.
func ParseDuration(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = "0.0.0.0:9445"
	}
	if cfg.Auth.JWTAudience == "" {
		cfg.Auth.JWTAudience = "bridge"
	}
	if cfg.Auth.JWTMaxTTL == "" {
		cfg.Auth.JWTMaxTTL = "5m"
	}
	if cfg.Sessions.MaxPerProject == 0 {
		cfg.Sessions.MaxPerProject = 5
	}
	if cfg.Sessions.MaxGlobal == 0 {
		cfg.Sessions.MaxGlobal = 20
	}
	if cfg.Sessions.EventBufferSize == 0 {
		cfg.Sessions.EventBufferSize = 10000
	}
	if cfg.Sessions.StopGracePeriod == "" {
		cfg.Sessions.StopGracePeriod = "10s"
	}
	if cfg.Sessions.IdleTimeout == "" {
		cfg.Sessions.IdleTimeout = "30m"
	}
	if cfg.Input.MaxSizeBytes == 0 {
		cfg.Input.MaxSizeBytes = 65536
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}
}
