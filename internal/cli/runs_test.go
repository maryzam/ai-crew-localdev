package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/telemetry"
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
	for _, expected := range []string{"abcdef12", "passed", "codex", "gpt-5", "owner/repo"} {
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
}

func testIntPointer(value int) *int {
	return &value
}
