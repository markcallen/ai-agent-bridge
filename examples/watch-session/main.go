package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	target := flag.String("target", "macbook.tail6198c2.ts.net:9445", "bridge gRPC address")

	caBundle := flag.String("cacert", "/home/marka/.ai-agent-bridge/certs/step-ca-root.crt", "path to CA bundle")
	cert := flag.String("cert", "/home/marka/.ai-agent-bridge/certs/do-dev2.crt", "path to client certificate")
	key := flag.String("key", "/home/marka/.ai-agent-bridge/certs/do-dev2.key", "path to client private key")

	jwtKey := flag.String("jwt-key", "jwt-signing.key", "path to Ed25519 JWT signing key")
	jwtIssuer := flag.String("jwt-issuer", "do-dev2", "JWT issuer claim")

	timeout := flag.Duration("timeout", 30*time.Minute, "watch timeout")

	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: watch-session [flags] <session-id>")
		os.Exit(1)
	}
	sessionID := flag.Arg(0)

	client, err := bridgeclient.New(
		bridgeclient.WithTarget(*target),
		bridgeclient.WithTimeout(*timeout),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: *caBundle,
			CertPath:     *cert,
			KeyPath:      *key,
			ServerName:   "server",
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: *jwtKey,
			Issuer:         *jwtIssuer,
			Audience:       "bridge",
		}),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	stream, err := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  uuid.NewString(),
		AfterSeq:  0,
		Role:      bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "attach: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
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
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "\r\nstream error: %v\r\n", err)
		os.Exit(1)
	}
}
