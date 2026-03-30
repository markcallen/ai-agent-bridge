package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	target := flag.String("target", "127.0.0.1:9445", "bridge gRPC address")
	project := flag.String("project", "dev", "project ID")
	provider := flag.String("provider", "claude", "interactive provider name")
	timeout := flag.Duration("timeout", 30*time.Minute, "session timeout")
	cacert := flag.String("cacert", "", "path to CA bundle")
	cert := flag.String("cert", "", "path to client certificate")
	key := flag.String("key", "", "path to client private key")
	servername := flag.String("servername", "", "TLS server name override")
	jwtKey := flag.String("jwt-key", "", "path to Ed25519 JWT signing key")
	jwtIssuer := flag.String("jwt-issuer", "", "JWT issuer claim")
	jwtAudience := flag.String("jwt-audience", "bridge", "JWT audience claim")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: chat [flags] <repo-path>")
		os.Exit(1)
	}
	repoPath := flag.Arg(0)

	opts := []bridgeclient.Option{
		bridgeclient.WithTarget(*target),
		bridgeclient.WithTimeout(*timeout),
	}
	if *cacert != "" && *cert != "" && *key != "" {
		opts = append(opts, bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: *cacert,
			CertPath:     *cert,
			KeyPath:      *key,
			ServerName:   *servername,
		}))
	}
	if *jwtKey != "" {
		opts = append(opts, bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: *jwtKey,
			Issuer:         *jwtIssuer,
			Audience:       *jwtAudience,
		}))
	}

	client, err := bridgeclient.New(opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	client.SetProject(*project)

	cols, rows := currentTTYSize()
	sessionID := uuid.NewString()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if _, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   *project,
		SessionId:   sessionID,
		RepoPath:    repoPath,
		Provider:    *provider,
		InitialCols: cols,
		InitialRows: rows,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start session: %v\n", err)
		os.Exit(1)
	}

	restoreTTY, err := setRawTTY()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to configure local tty: %v\n", err)
		os.Exit(1)
	}
	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(restoreTTY)
	}
	defer restore()

	stream, err := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  uuid.NewString(),
		AfterSeq:  0,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to attach session: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGWINCH)
	defer signal.Stop(sigCh)
	go func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGWINCH:
				cols, rows := currentTTYSize()
				_, _ = client.ResizeSession(context.Background(), &bridgev1.ResizeSessionRequest{
					SessionId: sessionID,
					ClientId:  stream.ClientID(),
					Cols:      cols,
					Rows:      rows,
				})
			default:
				cancel()
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
				_, _ = client.StopSession(stopCtx, &bridgev1.StopSessionRequest{
					SessionId: sessionID,
					Force:     true,
				})
				stopCancel()
				restore()
				os.Exit(0)
			}
		}
	}()

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				data := normalizeTTYInput(buf[:n])
				_, _ = client.WriteInput(context.Background(), &bridgev1.WriteInputRequest{
					SessionId: sessionID,
					ClientId:  stream.ClientID(),
					Data:      data,
				})
			}
			if err != nil {
				if err != io.EOF {
					fmt.Fprintf(os.Stderr, "\r\nstdin read failed: %v\r\n", err)
				}
				return
			}
		}
	}()

	err = stream.RecvAll(ctx, func(ev *bridgev1.AttachSessionEvent) error {
		switch ev.Type {
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT:
			_, err := os.Stdout.Write(ev.Payload)
			return err
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_REPLAY_GAP:
			_, err := fmt.Fprintf(os.Stderr, "\r\n[bridge] replay gap: oldest=%d last=%d\r\n", ev.OldestSeq, ev.LastSeq)
			return err
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ERROR:
			return errors.New(ev.Error)
		default:
			return nil
		}
	})
	restore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\r\nstream failed: %v\r\n", err)
		os.Exit(1)
	}
}

func setRawTTY() (func(), error) {
	if _, err := os.Stat("/dev/tty"); err != nil {
		return func() {}, err
	}
	out, err := exec.Command("stty", "-F", "/dev/tty", "-g").Output()
	if err != nil {
		return func() {}, err
	}
	state := string(bytesTrimSpace(out))
	if err := exec.Command("stty", "-F", "/dev/tty", "-icanon", "-echo", "min", "1", "time", "0").Run(); err != nil {
		return func() {}, err
	}
	return func() {
		_ = exec.Command("stty", "-F", "/dev/tty", state).Run()
	}, nil
}

func bytesTrimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	for len(b) > 0 && (b[0] == '\n' || b[0] == '\r' || b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	return b
}

func normalizeTTYInput(b []byte) []byte {
	data := append([]byte(nil), b...)
	for i := range data {
		if data[i] == '\n' {
			data[i] = '\r'
		}
	}
	return data
}

func currentTTYSize() (uint32, uint32) {
	ws, err := pty.GetsizeFull(os.Stdin)
	if err != nil {
		return 120, 40
	}
	return uint32(ws.Cols), uint32(ws.Rows)
}
