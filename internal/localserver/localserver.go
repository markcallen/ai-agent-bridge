// Package localserver provides an embeddable gRPC bridge server for local
// (single-machine) use. It runs without TLS or JWT auth on a unix-domain
// socket and auto-detects installed AI agent CLIs.
package localserver

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/auth"
	"github.com/markcallen/ai-agent-bridge/internal/bridge"
	"github.com/markcallen/ai-agent-bridge/internal/provider"
	"github.com/markcallen/ai-agent-bridge/internal/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// StateDir returns the ai-agent-bridge state directory. It respects the
// AI_AGENT_BRIDGE_STATE_DIR environment variable for testing; otherwise
// defaults to ~/.ai-agent-bridge.
func StateDir() string {
	if dir := os.Getenv("AI_AGENT_BRIDGE_STATE_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".ai-agent-bridge")
}

// SocketPath returns the default unix socket path.
func SocketPath() string {
	return filepath.Join(StateDir(), "server.sock")
}

// PIDPath returns the path to the server PID file.
func PIDPath() string {
	return filepath.Join(StateDir(), "server.pid")
}

// AddrPath returns the path to the server address file (TCP fallback on Windows).
func AddrPath() string {
	return filepath.Join(StateDir(), "server.addr")
}

// Server wraps all the components needed for a local bridge server.
type Server struct {
	grpcServer *grpc.Server
	supervisor *bridge.Supervisor
	listener   net.Listener
	logger     *slog.Logger
	stateDir   string
	mu         sync.Mutex
	stopped    bool
}

// Config controls local server behaviour.
type Config struct {
	// StateDir overrides the default ~/.ai-agent-bridge directory.
	StateDir string
	// Logger overrides the default logger. Nil uses a discard logger.
	Logger *slog.Logger
	// AllowedPaths restricts which repo paths sessions may use.
	// Empty means allow all.
	AllowedPaths []string
}

// Start launches a local bridge gRPC server. It listens on a unix socket
// (or TCP localhost on Windows) and writes a PID file for discovery.
func Start(cfg Config) (*Server, error) {
	stateDir := cfg.StateDir
	if stateDir == "" {
		stateDir = StateDir()
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create state dir %q: %w", stateDir, err)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}

	// Build provider registry from auto-detected CLIs.
	registry := bridge.NewRegistry()
	for _, pd := range detectProviders() {
		p := provider.NewStdioProvider(provider.StdioConfig{
			ProviderID:     pd.ID,
			Binary:         pd.Binary,
			DefaultArgs:    pd.Args,
			StartupTimeout: pd.StartupTimeout,
			StopGrace:      10 * time.Second,
			StartupProbe:   pd.StartupProbe,
			PromptPattern:  pd.PromptPattern,
			RequiredEnv:    pd.RequiredEnv,
			StreamJSON:     pd.StreamJSON,
		})
		if err := registry.Register(p); err != nil {
			logger.Warn("skip provider", "provider", pd.ID, "error", err)
			continue
		}
		logger.Info("registered provider", "provider", pd.ID, "binary", pd.Binary)
	}

	// Always register the echo provider for testing.
	echoProv := provider.NewStdioProvider(provider.StdioConfig{
		ProviderID:     "echo",
		Binary:         "cat",
		StartupTimeout: 5 * time.Second,
		StopGrace:      2 * time.Second,
		StartupProbe:   "none",
	})
	if err := registry.Register(echoProv); err != nil {
		logger.Debug("echo provider already registered", "error", err)
	}

	// Policy
	policy := bridge.Policy{
		MaxPerProject: 10,
		MaxGlobal:     20,
		MaxInputBytes: 65536,
		AllowedPaths:  cfg.AllowedPaths,
	}

	// Supervisor
	sup := bridge.NewSupervisor(registry, policy, 8<<20, 30*time.Minute)

	// Server instance ID
	instanceID := generateInstanceID()

	// gRPC server with anonymous passthrough auth (local mode).
	grpcOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(auth.UnaryPassthroughInterceptor()),
		grpc.ChainStreamInterceptor(auth.StreamPassthroughInterceptor()),
	}
	grpcServer := grpc.NewServer(grpcOpts...)

	bridgeServer := server.New(sup, registry, logger, server.RateLimitConfig{
		GlobalRPS:                  100,
		GlobalBurst:                200,
		StartSessionPerClientRPS:   5,
		StartSessionPerClientBurst: 10,
		SendInputPerSessionRPS:     20,
		SendInputPerSessionBurst:   50,
	}, instanceID, nil)
	bridgev1.RegisterBridgeServiceServer(grpcServer, bridgeServer)

	// Listen on unix socket (TCP fallback on Windows).
	ln, listenAddr, err := listen(stateDir)
	if err != nil {
		sup.Close()
		return nil, fmt.Errorf("listen: %w", err)
	}

	// Write PID file.
	pidFile := filepath.Join(stateDir, "server.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		_ = ln.Close()
		sup.Close()
		return nil, fmt.Errorf("write pid file: %w", err)
	}

	// Write address file (useful for TCP fallback).
	addrFile := filepath.Join(stateDir, "server.addr")
	if err := os.WriteFile(addrFile, []byte(listenAddr), 0o644); err != nil {
		_ = ln.Close()
		sup.Close()
		return nil, fmt.Errorf("write addr file: %w", err)
	}

	logger.Info("local server starting", "addr", listenAddr, "pid", os.Getpid())

	s := &Server{
		grpcServer: grpcServer,
		supervisor: sup,
		listener:   ln,
		logger:     logger,
		stateDir:   stateDir,
	}

	go func() {
		if err := grpcServer.Serve(ln); err != nil {
			logger.Error("grpc serve", "error", err)
		}
	}()

	return s, nil
}

// Addr returns the listener address (unix socket path or TCP address).
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Target returns the gRPC dial target for this server.
func (s *Server) Target() string {
	addr := s.listener.Addr()
	if addr.Network() == "unix" {
		return "unix://" + addr.String()
	}
	return addr.String()
}

// Stop gracefully shuts down the server and cleans up state files.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true

	s.logger.Info("stopping local server")
	s.grpcServer.GracefulStop()
	s.supervisor.Close()
	_ = s.listener.Close()

	// Clean up state files.
	_ = os.Remove(filepath.Join(s.stateDir, "server.pid"))
	_ = os.Remove(filepath.Join(s.stateDir, "server.addr"))
	_ = os.Remove(filepath.Join(s.stateDir, "server.sock"))
	_ = os.Remove(filepath.Join(s.stateDir, "server.lock"))
}

// listen creates the appropriate listener for the platform.
// On unix, it acquires an exclusive lockfile before replacing the socket
// to prevent a concurrent start from unlinking an active listener.
func listen(stateDir string) (net.Listener, string, error) {
	if runtime.GOOS == "windows" {
		// Windows: use TCP on localhost with a random port.
		// NOTE: local mode disables TLS/JWT, so any local process that
		// discovers the port can call RPCs. This is a known limitation;
		// consider a named pipe with ACLs for hardened Windows support.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, "", err
		}
		return ln, ln.Addr().String(), nil
	}

	// Unix socket for macOS and Linux.
	sockPath := filepath.Join(stateDir, "server.sock")
	lockPath := filepath.Join(stateDir, "server.lock")

	// Acquire an exclusive lockfile so concurrent starts don't race on
	// socket removal. The lock is released when the file is closed (on
	// process exit or when Stop removes the socket).
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("open lockfile: %w", err)
	}
	if err := acquireLock(lockFile); err != nil {
		_ = lockFile.Close()
		return nil, "", fmt.Errorf("acquire lock (another server starting?): %w", err)
	}

	// Safe to remove a stale socket now that we hold the lock.
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		_ = lockFile.Close()
		return nil, "", err
	}
	// Wrap the listener so it holds a reference to the lockfile. The flock
	// is released when Close() is called (via Server.Stop or process exit).
	return &lockedListener{Listener: ln, lockFile: lockFile}, sockPath, nil
}

// lockedListener wraps a net.Listener and holds a reference to a lockfile.
// Closing the listener also closes the lockfile, releasing the flock.
type lockedListener struct {
	net.Listener
	lockFile *os.File
}

func (l *lockedListener) Close() error {
	listenerErr := l.Listener.Close()
	lockErr := l.lockFile.Close()
	if listenerErr != nil {
		return listenerErr
	}
	return lockErr
}

// IsServerRunning checks if a local server is already running by probing
// the socket/address file.
func IsServerRunning(stateDir string) bool {
	if stateDir == "" {
		stateDir = StateDir()
	}
	target := discoverTarget(stateDir)
	if target == "" {
		return false
	}
	return probeHealth(target)
}

// DiscoverTarget returns the gRPC dial target for an existing local server,
// or empty string if none is found/reachable.
func DiscoverTarget(stateDir string) string {
	if stateDir == "" {
		stateDir = StateDir()
	}
	target := discoverTarget(stateDir)
	if target == "" {
		return ""
	}
	if !probeHealth(target) {
		return ""
	}
	return target
}

func discoverTarget(stateDir string) string {
	// Try unix socket first.
	sockPath := filepath.Join(stateDir, "server.sock")
	if _, err := os.Stat(sockPath); err == nil {
		return "unix://" + sockPath
	}
	// Fall back to TCP address file.
	addrData, err := os.ReadFile(filepath.Join(stateDir, "server.addr"))
	if err != nil {
		return ""
	}
	addr := strings.TrimSpace(string(addrData))
	if addr == "" {
		return ""
	}
	// If it looks like a unix path, prefix it.
	if strings.HasPrefix(addr, "/") {
		return "unix://" + addr
	}
	return addr
}

func probeHealth(target string) bool {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := bridgev1.NewBridgeServiceClient(conn)
	_, err = client.Health(ctx, &bridgev1.HealthRequest{})
	return err == nil
}

// providerDef describes a provider that can be auto-detected.
type providerDef struct {
	ID             string
	Binary         string
	Args           []string
	StartupTimeout time.Duration
	StartupProbe   string
	PromptPattern  string
	RequiredEnv    []string
	StreamJSON     bool
}

func detectProviders() []providerDef {
	var found []providerDef
	for _, pd := range knownProviders() {
		if _, err := exec.LookPath(pd.Binary); err != nil {
			continue
		}
		found = append(found, pd)
	}
	return found
}

func knownProviders() []providerDef {
	return []providerDef{
		{
			ID:             "claude",
			Binary:         "claude",
			Args:           []string{"--verbose"},
			StartupTimeout: 60 * time.Second,
			StartupProbe:   "prompt",
			PromptPattern:  `(?m)(❯|>\s*$)`,
			RequiredEnv:    []string{"ANTHROPIC_API_KEY"},
		},
		{
			ID:             "codex",
			Binary:         "codex",
			Args:           nil,
			StartupTimeout: 60 * time.Second,
			StartupProbe:   "prompt",
			PromptPattern:  `(?m)(>\s*$|›)`,
			RequiredEnv:    []string{"OPENAI_API_KEY"},
		},
		{
			ID:             "opencode",
			Binary:         "opencode",
			Args:           nil,
			StartupTimeout: 60 * time.Second,
			StartupProbe:   "output",
			PromptPattern:  `❯`,
			RequiredEnv:    []string{"OPENAI_API_KEY"},
		},
		{
			ID:             "gemini",
			Binary:         "gemini",
			Args:           nil,
			StartupTimeout: 60 * time.Second,
			StartupProbe:   "prompt",
			PromptPattern:  `^\s*>\s*$`,
			RequiredEnv:    []string{"GEMINI_API_KEY"},
		},
	}
}

func generateInstanceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
