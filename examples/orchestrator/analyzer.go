package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// decision is the orchestrator's verdict after analyzing buffered output.
type decision string

const (
	decisionDone    decision = "done"    // agent completed the task
	decisionStuck   decision = "stuck"   // agent is blocked and needs corrective input
	decisionWorking decision = "working" // agent is still making progress
)

// analyzer uses an OpenAI LLM to decide what the orchestrator should do next.
type analyzer struct {
	client *openai.Client
	model  string
	log    io.Writer
}

func newAnalyzer(apiKey, model string, log io.Writer) *analyzer {
	return &analyzer{
		client: openai.NewClient(apiKey),
		model:  model,
		log:    log,
	}
}

// analyze sends the recent terminal output to the LLM and returns:
//   - decision: done | stuck | working
//   - corrective: if stuck, the input to send to unblock the agent (else "")
//   - agentIdle: true when no new output arrived during the last watch interval
func (a *analyzer) analyze(ctx context.Context, task, output string, agentIdle bool) (decision, string, error) {
	prompt := buildAnalysisPrompt(task, output, agentIdle)

	_, _ = fmt.Fprintf(a.log, "\n=== [%s] SYSTEM PROMPT ===\n%s\n\n=== USER PROMPT ===\n%s\n\n",
		time.Now().Format(time.RFC3339), systemPrompt, prompt)

	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: systemPrompt,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		MaxCompletionTokens: 512,
		ReasoningEffort:     "high",
	})
	if err != nil {
		return decisionWorking, "", fmt.Errorf("openai: %w", err)
	}

	if len(resp.Choices) == 0 {
		return decisionWorking, "", fmt.Errorf("openai: empty response")
	}

	raw := resp.Choices[0].Message.Content
	_, _ = fmt.Fprintf(a.log, "=== OPENAI RESPONSE (%s) ===\n%s\n\n", a.model, raw)

	return parseDecision(raw)
}

const systemPrompt = `You are an orchestrator monitoring an AI coding agent (Claude Code) running in a terminal.
Your job is to read the terminal output and decide what to do next.

Respond with exactly one of these formats:

  DONE
  (if the agent has fully completed the task and there is nothing left to do)

  WORKING
  (if the agent is actively making progress — writing code, running tests, thinking, etc.)

  STUCK: <corrective input>
  (if the agent is blocked, waiting for input, hit an error loop, or appears idle
   with work remaining — provide the exact text to send to unblock it)

## Key signals to look for

The agent is DONE when:
- The terminal shows the Claude Code input prompt (❯) on its own line at the bottom,
  AND the task output above it shows completion (e.g. test results, coverage summary,
  a summary of changes made, or a statement that the work is finished).
- The agent explicitly states it has completed the task.

The agent is STUCK when:
- The ❯ prompt is visible but the task was never started or only partially completed.
- The same error or output has repeated multiple times with no progress.
- The NOTE says no new output was received AND the ❯ prompt is visible without a clear completion.

The agent is WORKING when:
- You see spinner characters (✻ ✽ ✶ ⠂ ⠐), "thinking", "Brewing", "Enchanting", or similar.
- Test output, file edits, or tool calls are actively streaming.
- No ❯ prompt is visible yet.

Be conservative: only say DONE when the task is clearly finished.
Only say STUCK when idle with work remaining.
Default to WORKING when uncertain.`

func buildAnalysisPrompt(task, output string, agentIdle bool) string {
	const maxOutput = 8000
	if len(output) > maxOutput {
		output = "...[truncated]\n" + output[len(output)-maxOutput:]
	}
	idleNote := ""
	if agentIdle {
		idleNote = "\n\nNOTE: No new terminal output was received during the last observation interval. The agent may be idle and waiting for input."
	}
	return fmt.Sprintf("TASK:\n%s\n\nRECENT TERMINAL OUTPUT:\n%s%s", task, output, idleNote)
}

// parseDecision interprets the LLM's free-text response into a typed decision.
func parseDecision(raw string) (decision, string, error) {
	text := strings.TrimSpace(raw)
	upper := strings.ToUpper(text)

	switch {
	case upper == "DONE" || strings.HasPrefix(upper, "DONE\n") || strings.HasPrefix(upper, "DONE "):
		return decisionDone, "", nil

	case upper == "WORKING" || strings.HasPrefix(upper, "WORKING\n") || strings.HasPrefix(upper, "WORKING "):
		return decisionWorking, "", nil

	case strings.HasPrefix(upper, "STUCK:"):
		// Extract everything after "STUCK:" as the corrective input.
		corrective := strings.TrimSpace(text[len("STUCK:"):])
		if corrective == "" {
			corrective = "continue"
		}
		return decisionStuck, corrective, nil

	default:
		// Unrecognized format — treat as still working to avoid false positives.
		return decisionWorking, "", fmt.Errorf("unrecognized decision format: %q", text)
	}
}
