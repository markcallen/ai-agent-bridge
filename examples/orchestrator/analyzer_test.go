package main

import (
	"strings"
	"testing"
)

func TestParseDecision(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		wantDecision  decision
		wantCorr      string
		wantErrSubstr string
	}{
		{
			name:         "done exact",
			raw:          "DONE",
			wantDecision: decisionDone,
		},
		{
			name:         "done lowercase",
			raw:          "done",
			wantDecision: decisionDone,
		},
		{
			name:         "done with trailing newline",
			raw:          "DONE\nsome extra",
			wantDecision: decisionDone,
		},
		{
			name:         "done with trailing space",
			raw:          "DONE some trailing",
			wantDecision: decisionDone,
		},
		{
			name:         "working exact",
			raw:          "WORKING",
			wantDecision: decisionWorking,
		},
		{
			name:         "working lowercase",
			raw:          "working",
			wantDecision: decisionWorking,
		},
		{
			name:         "working with trailing text",
			raw:          "WORKING\nstill going",
			wantDecision: decisionWorking,
		},
		{
			name:         "stuck with corrective",
			raw:          "STUCK: press enter to continue",
			wantDecision: decisionStuck,
			wantCorr:     "press enter to continue",
		},
		{
			name:         "stuck lowercase prefix",
			raw:          "stuck: type q to quit",
			wantDecision: decisionStuck,
			wantCorr:     "type q to quit",
		},
		{
			name:         "stuck with empty corrective defaults to continue",
			raw:          "STUCK:",
			wantDecision: decisionStuck,
			wantCorr:     "continue",
		},
		{
			name:          "unrecognized format",
			raw:           "MAYBE do something",
			wantDecision:  decisionWorking,
			wantErrSubstr: "unrecognized decision format",
		},
		{
			name:          "empty string",
			raw:           "",
			wantDecision:  decisionWorking,
			wantErrSubstr: "unrecognized decision format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, corr, err := parseDecision(tt.raw)
			if got != tt.wantDecision {
				t.Errorf("decision = %q, want %q", got, tt.wantDecision)
			}
			if corr != tt.wantCorr {
				t.Errorf("corrective = %q, want %q", corr, tt.wantCorr)
			}
			if tt.wantErrSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Errorf("err = %v, want substring %q", err, tt.wantErrSubstr)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestBuildAnalysisPrompt(t *testing.T) {
	task := "write a hello world test"
	output := "some terminal output"

	t.Run("without idle note", func(t *testing.T) {
		prompt := buildAnalysisPrompt(task, output, false)
		if !strings.Contains(prompt, task) {
			t.Error("prompt missing task")
		}
		if !strings.Contains(prompt, output) {
			t.Error("prompt missing output")
		}
		if strings.Contains(prompt, "NOTE:") {
			t.Error("prompt should not contain idle NOTE when agentIdle=false")
		}
	})

	t.Run("with idle note", func(t *testing.T) {
		prompt := buildAnalysisPrompt(task, output, true)
		if !strings.Contains(prompt, "NOTE:") {
			t.Error("prompt should contain idle NOTE when agentIdle=true")
		}
	})

	t.Run("truncates long output", func(t *testing.T) {
		longOutput := strings.Repeat("x", 10000)
		prompt := buildAnalysisPrompt(task, longOutput, false)
		if strings.Contains(prompt, longOutput) {
			t.Error("long output should be truncated")
		}
		if !strings.Contains(prompt, "...[truncated]") {
			t.Error("truncated output should contain truncation marker")
		}
	})
}
