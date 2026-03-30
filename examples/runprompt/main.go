package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	target := flag.String("target", "127.0.0.1:9445", "bridge gRPC address")
	project := flag.String("project", "dev", "project ID")
	timeout := flag.Duration("timeout", 5*time.Minute, "prompt timeout")
	flag.Parse()

	if flag.NArg() < 3 {
		fmt.Fprintln(os.Stderr, "usage: runprompt [flags] <provider> <repo-path> <prompt>")
		os.Exit(1)
	}
	provider := flag.Arg(0)
	repoPath := flag.Arg(1)
	prompt := flag.Arg(2)

	client, err := bridgeclient.New(
		bridgeclient.WithTarget(*target),
		bridgeclient.WithTimeout(*timeout),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	client.SetProject(*project)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	sessionID := uuid.NewString()
	if _, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   *project,
		SessionId:   sessionID,
		RepoPath:    repoPath,
		Provider:    provider,
		InitialCols: 120,
		InitialRows: 40,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}

	stream, err := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  uuid.NewString(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "attach: %v\n", err)
		os.Exit(1)
	}

	if _, err := client.WriteInput(ctx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  stream.ClientID(),
		Data:      []byte(prompt + "\n"),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}

	err = stream.RecvAll(ctx, func(ev *bridgev1.AttachSessionEvent) error {
		if ev.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT {
			_, err := os.Stdout.Write(ev.Payload)
			return err
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream: %v\n", err)
		os.Exit(1)
	}
}
