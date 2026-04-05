package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/auth"
	"github.com/markcallen/ai-agent-bridge/internal/bridge"
	"github.com/markcallen/ai-agent-bridge/internal/config"
	"github.com/markcallen/ai-agent-bridge/internal/pki"
	"github.com/markcallen/ai-agent-bridge/internal/provider"
	"github.com/markcallen/ai-agent-bridge/internal/redact"
	"github.com/markcallen/ai-agent-bridge/internal/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	configPath := flag.String("config", "config/bridge.yaml", "Path to configuration file")
	flag.Parse()

	bootstrapLogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		bootstrapLogger.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	if err := config.ValidateProviderEnv(cfg); err != nil {
		bootstrapLogger.Error("provider environment validation failed", "error", err)
		os.Exit(1)
	}
	redactor, err := redact.New(cfg.Logging.RedactPatterns)
	if err != nil {
		bootstrapLogger.Error("failed to compile redact patterns", "error", err)
		os.Exit(1)
	}
	logger := newLogger(cfg, redactor)

	// Set up provider registry
	registry := bridge.NewRegistry()
	for name, pcfg := range cfg.Providers {
		p := provider.NewStdioProvider(provider.StdioConfig{
			ProviderID:     name,
			Binary:         pcfg.Binary,
			DefaultArgs:    pcfg.Args,
			StartupTimeout: config.ParseDuration(pcfg.StartupTimeout, 30e9),
			StopGrace:      config.ParseDuration(cfg.Sessions.StopGracePeriod, 10e9),
			StartupProbe:   pcfg.StartupProbe,
			PromptPattern:  pcfg.PromptPattern,
			RequiredEnv:    pcfg.RequiredEnv,
		})
		if err := registry.Register(p); err != nil {
			logger.Error("register provider", "provider", name, "error", err)
			os.Exit(1)
		}
		logger.Info("registered provider", "provider", name, "binary", pcfg.Binary)
	}

	// Log provider versions at startup (5s timeout per provider).
	for name := range cfg.Providers {
		if p, err := registry.Get(name); err == nil {
			vCtx, vCancel := context.WithTimeout(context.Background(), 5*time.Second)
			v, vErr := p.Version(vCtx)
			vCancel()
			if vErr != nil {
				logger.Info("failed to get provider version", "provider", name, "error", vErr)
				continue
			}
			logger.Info("provider version", "provider", name, "version", v)
		}
	}
	for name := range cfg.Providers {
		pcfg := cfg.Providers[name]
		if !pcfg.ShouldValidateStartup() {
			logger.Info("provider startup validation skipped by config", "provider", name)
			continue
		}
		if p, err := registry.Get(name); err == nil {
			checkCtx, cancel := context.WithTimeout(context.Background(), p.StartupTimeout())
			err = p.ValidateStartup(checkCtx)
			cancel()
			if err != nil {
				logger.Error("provider startup validation failed", "provider", name, "error", err)
				os.Exit(1)
			}
			logger.Info("provider startup validation passed", "provider", name)
		}
	}

	// Set up policy
	policy := bridge.Policy{
		MaxPerProject: cfg.Sessions.MaxPerProject,
		MaxGlobal:     cfg.Sessions.MaxGlobal,
		MaxInputBytes: cfg.Input.MaxSizeBytes,
		AllowedPaths:  cfg.AllowedPaths,
	}

	// Generate a stable UUID for this daemon instance. Clients can compare
	// this value across Health calls to detect a restart (issue #6 phase 1).
	serverInstanceID := generateServerID()
	logger.Info("bridge instance id", "server_instance_id", serverInstanceID)

	// Set up optional session persistence store (issue #6 phase 1).
	var supOpts []bridge.SupervisorOption
	if cfg.Persistence.DBPath != "" {
		store, err := bridge.NewBoltSessionStore(cfg.Persistence.DBPath)
		if err != nil {
			logger.Error("open session store", "path", cfg.Persistence.DBPath, "error", err)
			os.Exit(1)
		}
		defer func() {
			if err := store.Close(); err != nil {
				logger.Warn("close session store", "error", err)
			}
		}()
		supOpts = append(supOpts, bridge.WithStore(store))
		logger.Info("session persistence enabled", "db_path", cfg.Persistence.DBPath)
	}

	// Set up supervisor
	idleTimeout := config.ParseDuration(cfg.Sessions.IdleTimeout, 30*time.Minute)
	sup := bridge.NewSupervisor(registry, policy, cfg.Sessions.EventBufferSize, idleTimeout, supOpts...)
	defer sup.Close()
	if err := sup.LoadHistory(); err != nil {
		logger.Warn("failed to load session history", "error", err)
	}

	// Set up JWT verifier
	verifier := &auth.JWTVerifier{
		Audience: cfg.Auth.JWTAudience,
		MaxTTL:   config.ParseDuration(cfg.Auth.JWTMaxTTL, 5*60e9),
		Keys:     make(map[string]ed25519.PublicKey),
	}
	for _, kc := range cfg.Auth.JWTPublicKeys {
		pub, err := pki.LoadEd25519PublicKey(kc.KeyPath)
		if err != nil {
			logger.Error("load jwt public key", "issuer", kc.Issuer, "error", err)
			os.Exit(1)
		}
		verifier.Keys[kc.Issuer] = pub
		logger.Info("loaded jwt public key", "issuer", kc.Issuer)
	}

	// Set up gRPC server options
	var grpcOpts []grpc.ServerOption

	// mTLS (optional: if TLS config is provided)
	if cfg.TLS.Cert != "" && cfg.TLS.Key != "" && cfg.TLS.CABundle != "" {
		tlsCfg, err := auth.ServerTLSConfig(auth.TLSConfig{
			CABundlePath: cfg.TLS.CABundle,
			CertPath:     cfg.TLS.Cert,
			KeyPath:      cfg.TLS.Key,
		})
		if err != nil {
			logger.Error("configure TLS", "error", err)
			os.Exit(1)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(tlsCfg)))
		logger.Info("mTLS enabled")
	} else {
		logger.Warn("TLS not configured - running without encryption (dev mode only)")
	}

	// JWT interceptors (only if keys are configured)
	if len(verifier.Keys) > 0 {
		grpcOpts = append(grpcOpts,
			grpc.ChainUnaryInterceptor(
				auth.UnaryJWTInterceptor(verifier, logger),
				auth.UnaryAuditInterceptor(logger),
			),
			grpc.ChainStreamInterceptor(
				auth.StreamJWTInterceptor(verifier, logger),
				auth.StreamAuditInterceptor(logger),
			),
		)
		logger.Info("JWT auth enabled", "issuers", len(verifier.Keys))
	} else {
		// No JWT keys: inject anonymous claims so RPCs function in dev mode.
		grpcOpts = append(grpcOpts,
			grpc.ChainUnaryInterceptor(auth.UnaryPassthroughInterceptor()),
			grpc.ChainStreamInterceptor(auth.StreamPassthroughInterceptor()),
		)
		logger.Warn("no JWT keys configured - auth disabled (dev mode only)")
	}

	grpcServer := grpc.NewServer(grpcOpts...)
	bridgeServer := server.New(sup, registry, logger, server.RateLimitConfig{
		GlobalRPS:                  cfg.RateLimits.GlobalRPS,
		GlobalBurst:                cfg.RateLimits.GlobalBurst,
		StartSessionPerClientRPS:   cfg.RateLimits.StartSessionPerClientRPS,
		StartSessionPerClientBurst: cfg.RateLimits.StartSessionPerClientBurst,
		SendInputPerSessionRPS:     cfg.RateLimits.SendInputPerSessionRPS,
		SendInputPerSessionBurst:   cfg.RateLimits.SendInputPerSessionBurst,
	}, serverInstanceID)
	bridgev1.RegisterBridgeServiceServer(grpcServer, bridgeServer)

	ln, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		logger.Error("listen", "error", err)
		os.Exit(1)
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("shutting down", "signal", sig.String())
		grpcServer.GracefulStop()
	}()

	logger.Info("bridge daemon starting", "listen", cfg.Server.Listen)
	if err := grpcServer.Serve(ln); err != nil {
		logger.Error("serve", "error", err)
		os.Exit(1)
	}
}

// generateServerID returns a random UUID (RFC 4122 v4) for this daemon instance.
func generateServerID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("generateServerID: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func newLogger(cfg *config.Config, redactor *redact.Redactor) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	options := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Value.Kind() == slog.KindString {
				a.Value = slog.StringValue(redactor.Redact(a.Value.String()))
			}
			return a
		},
	}
	if cfg.Logging.Format == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, options))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, options))
}
