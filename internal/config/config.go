package config

import (
	"fmt"
	"os"
	"strings"
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
	RateLimits   RateLimitsConfig          `yaml:"rate_limits"`
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
	MaxPerProject            int    `yaml:"max_per_project"`
	MaxGlobal                int    `yaml:"max_global"`
	IdleTimeout              string `yaml:"idle_timeout"`
	StopGracePeriod          string `yaml:"stop_grace_period"`
	EventBufferSize          int    `yaml:"event_buffer_size"`
	MaxSubscribersPerSession int    `yaml:"max_subscribers_per_session"`
	SubscriberTTL            string `yaml:"subscriber_ttl"`
}

type InputConfig struct {
	MaxSizeBytes int `yaml:"max_size_bytes"`
}

type RateLimitsConfig struct {
	GlobalRPS                  float64 `yaml:"global_rps"`
	GlobalBurst                int     `yaml:"global_burst"`
	StartSessionPerClientRPS   float64 `yaml:"start_session_per_client_rps"`
	StartSessionPerClientBurst int     `yaml:"start_session_per_client_burst"`
	SendInputPerSessionRPS     float64 `yaml:"send_input_per_session_rps"`
	SendInputPerSessionBurst   int     `yaml:"send_input_per_session_burst"`
}

type ProviderConfig struct {
	Binary         string   `yaml:"binary"`
	Args           []string `yaml:"args"`
	StartupTimeout string   `yaml:"startup_timeout"`
	RequiredEnv    []string `yaml:"required_env"`
	PTY            bool     `yaml:"pty"`
	StreamJSON     bool     `yaml:"stream_json"`
	// PromptPattern is a regex matched against PTY output lines. When it
	// matches the first time, AGENT_READY is emitted; on subsequent matches
	// after output, RESPONSE_COMPLETE is emitted.
	PromptPattern string `yaml:"prompt_pattern"`
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
	if err := validate(cfg); err != nil {
		return nil, err
	}
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
	if cfg.Sessions.MaxSubscribersPerSession == 0 {
		cfg.Sessions.MaxSubscribersPerSession = 10
	}
	if cfg.Sessions.SubscriberTTL == "" {
		cfg.Sessions.SubscriberTTL = "30m"
	}
	if cfg.Input.MaxSizeBytes == 0 {
		cfg.Input.MaxSizeBytes = 65536
	}
	if cfg.RateLimits.GlobalRPS == 0 {
		cfg.RateLimits.GlobalRPS = 50
	}
	if cfg.RateLimits.GlobalBurst == 0 {
		cfg.RateLimits.GlobalBurst = 100
	}
	if cfg.RateLimits.StartSessionPerClientRPS == 0 {
		cfg.RateLimits.StartSessionPerClientRPS = 1
	}
	if cfg.RateLimits.StartSessionPerClientBurst == 0 {
		cfg.RateLimits.StartSessionPerClientBurst = 3
	}
	if cfg.RateLimits.SendInputPerSessionRPS == 0 {
		cfg.RateLimits.SendInputPerSessionRPS = 5
	}
	if cfg.RateLimits.SendInputPerSessionBurst == 0 {
		cfg.RateLimits.SendInputPerSessionBurst = 20
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}
}

func validate(cfg *Config) error {
	if cfg.Server.Listen == "" {
		return fmt.Errorf("config: server.listen is required")
	}
	if cfg.Input.MaxSizeBytes <= 0 {
		return fmt.Errorf("config: input.max_size_bytes must be > 0")
	}
	if cfg.Sessions.MaxPerProject < 0 || cfg.Sessions.MaxGlobal < 0 {
		return fmt.Errorf("config: session limits must be >= 0")
	}
	if cfg.Sessions.EventBufferSize <= 0 {
		return fmt.Errorf("config: sessions.event_buffer_size must be > 0")
	}
	if cfg.Sessions.MaxSubscribersPerSession <= 0 {
		return fmt.Errorf("config: sessions.max_subscribers_per_session must be > 0")
	}
	if cfg.RateLimits.GlobalRPS <= 0 || cfg.RateLimits.GlobalBurst <= 0 {
		return fmt.Errorf("config: rate_limits.global_rps/global_burst must be > 0")
	}
	if cfg.RateLimits.StartSessionPerClientRPS <= 0 || cfg.RateLimits.StartSessionPerClientBurst <= 0 {
		return fmt.Errorf("config: rate_limits.start_session_per_client_rps/start_session_per_client_burst must be > 0")
	}
	if cfg.RateLimits.SendInputPerSessionRPS <= 0 || cfg.RateLimits.SendInputPerSessionBurst <= 0 {
		return fmt.Errorf("config: rate_limits.send_input_per_session_rps/send_input_per_session_burst must be > 0")
	}
	if _, err := time.ParseDuration(cfg.Auth.JWTMaxTTL); err != nil {
		return fmt.Errorf("config: auth.jwt_max_ttl: %w", err)
	}
	if _, err := time.ParseDuration(cfg.Sessions.IdleTimeout); err != nil {
		return fmt.Errorf("config: sessions.idle_timeout: %w", err)
	}
	if _, err := time.ParseDuration(cfg.Sessions.StopGracePeriod); err != nil {
		return fmt.Errorf("config: sessions.stop_grace_period: %w", err)
	}
	if _, err := time.ParseDuration(cfg.Sessions.SubscriberTTL); err != nil {
		return fmt.Errorf("config: sessions.subscriber_ttl: %w", err)
	}
	for name, provider := range cfg.Providers {
		if provider.Binary == "" {
			return fmt.Errorf("config: providers.%s.binary is required", name)
		}
		if provider.StartupTimeout != "" {
			if _, err := time.ParseDuration(provider.StartupTimeout); err != nil {
				return fmt.Errorf("config: providers.%s.startup_timeout: %w", name, err)
			}
		}
		for i, envName := range provider.RequiredEnv {
			if strings.TrimSpace(envName) == "" {
				return fmt.Errorf("config: providers.%s.required_env[%d] must not be empty", name, i)
			}
		}
	}
	return nil
}
