package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	root := &cobra.Command{
		Use:   "ai-agent-bridge-cli",
		Short: "AI Agent Bridge — run AI agents locally",
		Long: `ai-agent-bridge-cli starts a local bridge server and spawns AI agent sessions
in your terminal. The server auto-starts on first use and is shared
across terminal windows.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}

	root.AddCommand(
		newRunCmd(),
		newSessionCmd(),
		newServerCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
