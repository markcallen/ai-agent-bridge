package main

import (
	"crypto/ed25519"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/auth"
	"github.com/markcallen/ai-agent-bridge/internal/bridge"
	"github.com/markcallen/ai-agent-bridge/internal/config"
	"github.com/markcallen/ai-agent-bridge/internal/pki"
	"github.com/markcallen/ai-agent-bridge/internal/provider"
	"github.com/markcallen/ai-agent-bridge/internal/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	configPath := flag.String("config", "config/bridge.yaml", "Path to configuration file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Set up provider registry
	registry := bridge.NewRegistry()
	for name, pcfg := range cfg.Providers {
		p := provider.NewStdioProvider(provider.StdioConfig{
			ProviderID:     name,
			Binary:         pcfg.Binary,
			DefaultArgs:    pcfg.Args,
			StartupTimeout: config.ParseDuration(pcfg.StartupTimeout, 30e9),
		})
		if err := registry.Register(p); err != nil {
			logger.Error("register provider", "provider", name, "error", err)
			os.Exit(1)
		}
		logger.Info("registered provider", "provider", name, "binary", pcfg.Binary)
	}

	// Set up policy
	policy := bridge.Policy{
		MaxPerProject: cfg.Sessions.MaxPerProject,
		MaxGlobal:     cfg.Sessions.MaxGlobal,
		MaxInputBytes: cfg.Input.MaxSizeBytes,
		AllowedPaths:  cfg.AllowedPaths,
	}

	// Set up supervisor
	sup := bridge.NewSupervisor(registry, policy, cfg.Sessions.EventBufferSize)
	defer sup.Close()

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
			grpc.UnaryInterceptor(auth.UnaryJWTInterceptor(verifier)),
			grpc.StreamInterceptor(auth.StreamJWTInterceptor(verifier)),
		)
		logger.Info("JWT auth enabled", "issuers", len(verifier.Keys))
	} else {
		logger.Warn("no JWT keys configured - auth disabled (dev mode only)")
	}

	grpcServer := grpc.NewServer(grpcOpts...)
	bridgeServer := server.New(sup, registry, logger)
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

