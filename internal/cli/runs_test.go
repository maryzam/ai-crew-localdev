package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
	"github.com/spf13/cobra"
)

func TestRunsListAndShowExposeManagedRunHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", path)
	for _, key := range []string{"AI_AGENT_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT", "AI_AGENT_LANGFUSE_PUBLIC_KEY", "LANGFUSE_PUBLIC_KEY", "AI_AGENT_LANGFUSE_SECRET_KEY", "LANGFUSE_SECRET_KEY"} {
		t.Setenv(key, "")
	}
	recorder, err := telemetry.StartRun(telemetry.RunContext{
		RunID:        "run_abcdef1234567890",
		AgentName:    "codex",
		Repo:         "owner/repo",
		HostRepoPath: t.TempDir(),
		AgentCommand: []string{"codex", "--model", "gpt-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder.AgentStarted(1)
	recorder.AgentFinished(1, "passed", testIntPointer(0), time.Millisecond)
	totalTokens := int64(123)
	recorder.RecordUsage(telemetry.Usage{
		Status: "observed", TotalTokens: &totalTokens, Source: "native_otel",
		Scope: "run", Precision: "request", Confidence: "provider_reported",
	})
	recorder.Finish(telemetry.OutcomePassed, telemetry.PhaseAgent, testIntPointer(0), time.Millisecond)
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	listOutput := new(bytes.Buffer)
	listCommand := &cobra.Command{}
	listCommand.SetOut(listOutput)
	runsListJSON = false
	runsListLimit = 20
	if err := runRunsList(listCommand, nil); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"abcdef12", "passed", "codex", "gpt-5", "123", "owner/repo"} {
		if !strings.Contains(listOutput.String(), expected) {
			t.Errorf("list output missing %q: %s", expected, listOutput)
		}
	}

	showOutput := new(bytes.Buffer)
	showCommand := &cobra.Command{}
	showCommand.SetOut(showOutput)
	runsShowJSON = true
	if err := runRunsShow(showCommand, []string{"abcdef12"}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"run_id": "run_abcdef1234567890"`, `"provider": "openai"`, `"outcome": "passed"`} {
		if !strings.Contains(showOutput.String(), expected) {
			t.Errorf("show output missing %q: %s", expected, showOutput)
		}
	}

	analyzeOutput := new(bytes.Buffer)
	analyzeCommand := &cobra.Command{}
	analyzeCommand.SetOut(analyzeOutput)
	runsAnalyzeJSON = false
	runsAnalyzeSince = 24 * time.Hour
	runsAnalyzeHighTokens = 100
	runsAnalyzeRepeatedFailures = 2
	runsAnalyzeUnverifiedRuns = 1
	runsAnalyzeMaxFindings = 20
	if err := runRunsAnalyze(analyzeCommand, nil); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Adaptive optimization report", "codex", "123", "high_token_run", "weak_verification", "Advisory only"} {
		if !strings.Contains(analyzeOutput.String(), expected) {
			t.Errorf("analysis output missing %q: %s", expected, analyzeOutput)
		}
	}

	analyzeJSON := new(bytes.Buffer)
	analyzeCommand.SetOut(analyzeJSON)
	runsAnalyzeJSON = true
	if err := runRunsAnalyze(analyzeCommand, nil); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"schema_version": "1"`, `"high_token_threshold": 100`, `"kind": "high_token_run"`, `"truncated_findings": 0`} {
		if !strings.Contains(analyzeJSON.String(), expected) {
			t.Errorf("analysis JSON missing %q: %s", expected, analyzeJSON)
		}
	}
	for _, forbidden := range []string{`"rank"`, `"weight"`} {
		if strings.Contains(analyzeJSON.String(), forbidden) {
			t.Errorf("analysis JSON exposed internal field %q: %s", forbidden, analyzeJSON)
		}
	}
}

func testIntPointer(value int) *int {
	return &value
}
