// orchestrator drives an AI agent session on a remote machine to completion.
//
// It follows this loop:
//
//  1. Start   — launch the agent in the given repo directory
//  2. Send    — write the initial task prompt to the agent
//  3. Detach  — switch to watch-only mode and buffer output
//  4. Analyze — ask an OpenAI LLM whether the agent is done, stuck, or working
//  5. Act     — if done: stop; if stuck: re-attach and send corrective input; if working: keep watching
//
// Flags:
//
//	--machine   remote hostname or host:port (required)
//	--task      task description to send to the agent (required)
//	--provider  AI agent provider: claude | opencode | codex | gemini  (default: claude)
//	--project   bridge project ID  (default: local)
//	--model     OpenAI model for the orchestrator  (default: gpt-5.6)
//	--timeout   total session lifetime  (default: 30m)
//	--interval  how often to analyze output  (default: 15s)
//	--state-dir credential directory  (default: ~/.config/bridgectl)
//
// Environment:
//
//	OPENAI_API_KEY  required for the orchestrator LLM
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/orchael/bridgectl/internal/localserver"
	"github.com/orchael/bridgectl/pkg/bridgeclient"
)

func main() {
	machine := flag.String("machine", "", "remote hostname or host:port (required; port defaults to 9445)")
	task := flag.String("task", "", "task to send to the agent (required)")
	provider := flag.String("provider", "claude", "AI agent provider (claude, opencode, codex, gemini)")
	project := flag.String("project", "local", "bridge project ID")
	model := flag.String("model", "gpt-5.6", "OpenAI model for the orchestrator analyzer")
	timeout := flag.Duration("timeout", 30*time.Minute, "total session lifetime")
	interval := flag.Duration("interval", 15*time.Second, "how often the orchestrator analyzes buffered output")
	stateDir := flag.String("state-dir", "", "credential directory (default: ~/.config/bridgectl)")
	logFile := flag.String("log", "", "path to write LLM conversation log (default: orchestrator-<timestamp>.log)")
	flag.Parse()

	if *machine == "" {
		fmt.Fprintln(os.Stderr, "error: --machine is required")
		flag.Usage()
		os.Exit(1)
	}
	if *task == "" {
		fmt.Fprintln(os.Stderr, "error: --task is required")
		flag.Usage()
		os.Exit(1)
	}
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: repo-path argument is required")
		fmt.Fprintln(os.Stderr, "usage: orchestrator --machine <host> --task <task> [flags] <repo-path>")
		os.Exit(1)
	}
	repoPath := flag.Arg(0)

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "error: OPENAI_API_KEY environment variable is required")
		os.Exit(1)
	}

	sd := *stateDir
	if sd == "" {
		sd = localserver.StateDir()
	}

	client, err := buildRemoteClient(*machine, sd, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n\n", err)
		fmt.Fprintln(os.Stderr, "Enroll with the remote server first:")
		fmt.Fprintln(os.Stderr, "  bridgectl client init")
		os.Exit(1)
	}
	defer func() { _ = client.Close() }()
	client.SetProject(*project)

	logPath := *logFile
	if logPath == "" {
		logPath = fmt.Sprintf("orchestrator-%s.log", time.Now().Format("20060102-150405"))
	}
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot open log file %s: %v\n", logPath, err)
		os.Exit(1)
	}
	defer func() { _ = lf.Close() }()
	fmt.Fprintf(os.Stderr, "[orchestrator] logging LLM conversation to %s\n", logPath)

	analyzer := newAnalyzer(apiKey, *model, io.MultiWriter(os.Stderr, lf))

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\n[orchestrator] interrupted — cancelling")
		cancel()
	}()

	orch := &orchestrator{
		client:   client,
		analyzer: analyzer,
		machine:  *machine,
		repoPath: repoPath,
		provider: *provider,
		project:  *project,
		task:     *task,
		interval: *interval,
	}

	if err := orch.run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "[orchestrator] failed: %v\n", err)
		os.Exit(1)
	}
}

// orchestrator holds the dependencies and configuration for a single run.
type orchestrator struct {
	client   *bridgeclient.Client
	analyzer *analyzer
	machine  string
	repoPath string
	provider string
	project  string
	task     string
	interval time.Duration
}
