// Package localserver provides an embeddable gRPC bridge server for local
// and remote use. In local mode it runs without TLS on a unix-domain socket.
// In secure mode (--listen flag) it binds to a TCP address with mTLS + JWT.
package localserver

import (
	"context"
	"crypto/ed25519"
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
	"github.com/markcallen/ai-agent-bridge/internal/pki"
	"github.com/markcallen/ai-agent-bridge/internal/provider"
	"github.com/markcallen/ai-agent-bridge/internal/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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

// ServerMode represents how the server is running.
type ServerMode string

const (
	// ModeLocal is the default mode: unix socket, no auth.
	ModeLocal ServerMode = "local"
	// ModeSecure uses TCP + mTLS + JWT for remote access.
	ModeSecure ServerMode = "secure"
)

// ModePath returns the path to the server mode file.
func ModePath() string {
	return filepath.Join(StateDir(), "server.mode")
}

// DiscoverMode reads the server.mode file to determine how to connect.
// Returns ModeLocal if the file is missing or unreadable.
func DiscoverMode(stateDir string) ServerMode {
	if stateDir == "" {
		stateDir = StateDir()
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "server.mode"))
	if err != nil {
		return ModeLocal
	}
	mode := ServerMode(strings.TrimSpace(string(data)))
	if mode == ModeSecure {
		return ModeSecure
	}
	return ModeLocal
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

	// ListenAddr, when set, enables secure mode: the server binds to this
	// TCP address with mTLS + JWT instead of a unix socket. Example:
	// "10.0.0.1:9445" or "0.0.0.0:9445".
	ListenAddr string
	// ServerSANs are additional DNS names or IP addresses for the server
	// certificate. The host from ListenAddr is added automatically.
	ServerSANs []string
}

// Start launches a local bridge gRPC server. In local mode (default) it
// listens on a unix socket (or TCP localhost on Windows) without auth.
// In secure mode (ListenAddr set) it binds to TCP with mTLS + JWT.
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

	// Determine server mode and build gRPC options accordingly.
	mode := ModeLocal
	var grpcOpts []grpc.ServerOption

	if cfg.ListenAddr != "" {
		// Secure mode: TCP + mTLS + JWT.
		// TODO(windows): Secure mode (mTLS+JWT) is not yet supported on Windows.
		// Windows support requires named-pipe ACLs or equivalent transport security.
		if runtime.GOOS == "windows" {
			sup.Close()
			return nil, fmt.Errorf("secure mode (--listen) is not yet supported on Windows")
		}

		mode = ModeSecure

		// Auto-generate PKI material if not present.
		sans := buildServerSANs(cfg.ListenAddr, cfg.ServerSANs)
		mat, err := EnsurePKI(stateDir, sans, logger)
		if err != nil {
			sup.Close()
			return nil, fmt.Errorf("ensure PKI: %w", err)
		}

		secureOpts, err := buildSecureGRPCOpts(mat, logger)
		if err != nil {
			sup.Close()
			return nil, fmt.Errorf("build secure gRPC options: %w", err)
		}
		grpcOpts = secureOpts
	} else {
		// Local mode: unix socket, anonymous passthrough auth.
		grpcOpts = []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(auth.UnaryPassthroughInterceptor()),
			grpc.ChainStreamInterceptor(auth.StreamPassthroughInterceptor()),
		}
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

	// Listen: TCP for secure mode, unix socket for local mode.
	var ln net.Listener
	var listenAddr string
	var err error
	if mode == ModeSecure {
		ln, err = net.Listen("tcp", cfg.ListenAddr)
		if err != nil {
			sup.Close()
			return nil, fmt.Errorf("listen tcp %s: %w", cfg.ListenAddr, err)
		}
		listenAddr = ln.Addr().String()
	} else {
		ln, listenAddr, err = listen(stateDir)
		if err != nil {
			sup.Close()
			return nil, fmt.Errorf("listen: %w", err)
		}
	}

	// Write PID file.
	pidFile := filepath.Join(stateDir, "server.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		_ = ln.Close()
		sup.Close()
		return nil, fmt.Errorf("write pid file: %w", err)
	}

	// Write address file.
	addrFile := filepath.Join(stateDir, "server.addr")
	if err := os.WriteFile(addrFile, []byte(listenAddr), 0o644); err != nil {
		_ = ln.Close()
		sup.Close()
		return nil, fmt.Errorf("write addr file: %w", err)
	}

	// Write mode file so discovery knows how to connect.
	modeFile := filepath.Join(stateDir, "server.mode")
	if err := os.WriteFile(modeFile, []byte(string(mode)), 0o644); err != nil {
		_ = ln.Close()
		sup.Close()
		return nil, fmt.Errorf("write mode file: %w", err)
	}

	logger.Info("server starting", "mode", mode, "addr", listenAddr, "pid", os.Getpid())

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

// buildSecureGRPCOpts returns gRPC server options for mTLS + JWT mode.
func buildSecureGRPCOpts(mat *PKIMaterial, logger *slog.Logger) ([]grpc.ServerOption, error) {
	// TLS credentials with client cert verification.
	tlsCfg, err := auth.ServerTLSConfig(auth.TLSConfig{
		CABundlePath: mat.CABundlePath,
		CertPath:     mat.ServerCertPath,
		KeyPath:      mat.ServerKeyPath,
	})
	if err != nil {
		return nil, fmt.Errorf("server TLS config: %w", err)
	}

	// JWT verifier using the local signing key.
	pubKey, err := pki.LoadEd25519PublicKey(mat.JWTSigningPub)
	if err != nil {
		return nil, fmt.Errorf("load JWT public key: %w", err)
	}
	verifier := &auth.JWTVerifier{
		Keys:     map[string]ed25519.PublicKey{"local": pubKey},
		Audience: "bridge",
		MaxTTL:   10 * time.Minute,
	}

	return []grpc.ServerOption{
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.ChainUnaryInterceptor(
			auth.UnaryJWTInterceptor(verifier, logger),
			auth.UnaryAuditInterceptor(logger),
		),
		grpc.ChainStreamInterceptor(
			auth.StreamJWTInterceptor(verifier, logger),
			auth.StreamAuditInterceptor(logger),
		),
	}, nil
}

// buildServerSANs extracts the host from listenAddr and merges it with
// any additional SANs. Deduplicates entries.
func buildServerSANs(listenAddr string, extra []string) []string {
	seen := make(map[string]bool)
	var sans []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			sans = append(sans, s)
		}
	}

	// Extract host from listenAddr (e.g. "10.0.0.1:9445" → "10.0.0.1").
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		// Might be just a host without port.
		host = listenAddr
	}
	// Don't add wildcard addresses as SANs.
	if host != "" && host != "0.0.0.0" && host != "::" {
		add(host)
	}
	// Always include localhost and the CN "server" so TLS ServerName
	// verification works (Go ignores CN when SANs are present).
	add("server")
	add("127.0.0.1")
	add("localhost")

	for _, s := range extra {
		add(s)
	}
	return sans
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
	_ = os.Remove(filepath.Join(s.stateDir, "server.mode"))
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
	mode := DiscoverMode(stateDir)
	return probeHealth(target, mode, stateDir)
}

// DiscoverTarget returns the gRPC dial target and mode for an existing
// local server. Returns empty target if none is found/reachable.
func DiscoverTarget(stateDir string) (target string, mode ServerMode) {
	if stateDir == "" {
		stateDir = StateDir()
	}
	target = discoverTarget(stateDir)
	if target == "" {
		return "", ModeLocal
	}
	mode = DiscoverMode(stateDir)
	if !probeHealth(target, mode, stateDir) {
		return "", ModeLocal
	}
	return target, mode
}

func discoverTarget(stateDir string) string {
	// Try unix socket first (local mode).
	sockPath := filepath.Join(stateDir, "server.sock")
	if _, err := os.Stat(sockPath); err == nil {
		return "unix://" + sockPath
	}
	// Fall back to TCP address file (secure mode or Windows).
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

func probeHealth(target string, mode ServerMode, stateDir string) bool {
	var dialOpts []grpc.DialOption

	if mode == ModeSecure {
		mat := LoadPKIMaterial(stateDir)
		tlsCfg, err := auth.ClientTLSConfig(auth.TLSConfig{
			CABundlePath: mat.CABundlePath,
			CertPath:     mat.LocalClientCert,
			KeyPath:      mat.LocalClientKey,
			ServerName:   "server",
		})
		if err != nil {
			return false
		}
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.NewClient(target, dialOpts...)
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
