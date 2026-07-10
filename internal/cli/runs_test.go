package cli

import (
	"bytes"
	"github.com/maryzam/ai-crew-localdev/internal/app/adaptive"
	"github.com/maryzam/ai-crew-localdev/internal/app/adaptive/ledger"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
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
	recorder.VerifyStarted(1, "tests", "make test")
	recorder.VerifyFinished(1, "tests", "passed", "", testIntPointer(0), time.Millisecond)
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

	showTextOutput := new(bytes.Buffer)
	showTextCommand := &cobra.Command{}
	showTextCommand.SetOut(showTextOutput)
	runsShowJSON = false
	if err := runRunsShow(showTextCommand, []string{"abcdef12"}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Contract tests:", "passed (1 attempts)"} {
		if !strings.Contains(showTextOutput.String(), expected) {
			t.Errorf("text show output missing %q: %s", expected, showTextOutput)
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
	runsAnalyzeUnverifiedPercent = 80
	runsAnalyzeMaxFindings = 20
	if err := runRunsAnalyze(analyzeCommand, nil); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Adaptive optimization report", "TRUSTED", "codex", "123", "high_token_run", "weak_verification", "unverified_percent=100", "Advisory only"} {
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
	for _, expected := range []string{`"schema_version": "1"`, `"high_token_threshold": 100`, `"weak_verification_percent": 80`, `"kind": "high_token_run"`, `"truncated_findings": 0`} {
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

func TestAdaptiveFindingsLifecycle(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())

	for _, id := range []string{"run_ledgeraaaa000000000000000000001", "run_ledgeraaaa000000000000000000002"} {
		rec, err := telemetry.StartRun(telemetry.RunContext{RunID: id, AgentName: "codex", Repo: "owner/ledger-repo"})
		if err != nil {
			t.Fatal(err)
		}
		rec.AgentStarted(1)
		rec.AgentFinished(1, "failed", testIntPointer(2), time.Millisecond)
		rec.Finish(telemetry.OutcomeAgentFailed, telemetry.PhaseAgent, testIntPointer(2), time.Millisecond)
		if err := rec.Close(); err != nil {
			t.Fatal(err)
		}
	}

	analyzeOut := new(bytes.Buffer)
	analyzeCmd := &cobra.Command{}
	analyzeCmd.SetOut(analyzeOut)
	runsAnalyzeJSON = false
	runsAnalyzeSince = 24 * time.Hour
	runsAnalyzeHighTokens = adaptive.DefaultHighTokenThreshold
	runsAnalyzeRepeatedFailures = adaptive.DefaultRepeatedFailureRuns
	runsAnalyzeUnverifiedRuns = adaptive.DefaultWeakVerificationRuns
	runsAnalyzeUnverifiedPercent = adaptive.DefaultWeakVerificationPercent
	runsAnalyzeMaxFindings = adaptive.DefaultMaxFindings
	if err := runRunsAnalyze(analyzeCmd, nil); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if !strings.Contains(analyzeOut.String(), "(open)") {
		t.Fatalf("analyze output missing tracked open status:\n%s", analyzeOut)
	}

	ledgerFile, err := ledger.Load(paths.DefaultAdaptiveLedgerPath())
	if err != nil || len(ledgerFile.Entries) == 0 {
		t.Fatalf("ledger not persisted by analyze: %+v, %v", ledgerFile, err)
	}
	fingerprint := ledgerFile.Entries[0].Fingerprint

	acceptOut := new(bytes.Buffer)
	acceptCmd := &cobra.Command{}
	acceptCmd.SetOut(acceptOut)
	if err := runRunsFindingsAccept(acceptCmd, []string{fingerprint[:8]}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if !strings.Contains(acceptOut.String(), "accepted "+fingerprint) {
		t.Fatalf("accept output: %s", acceptOut)
	}

	secondOut := new(bytes.Buffer)
	secondCmd := &cobra.Command{}
	secondCmd.SetOut(secondOut)
	if err := runRunsAnalyze(secondCmd, nil); err != nil {
		t.Fatalf("second analyze: %v", err)
	}
	output := secondOut.String()
	if !strings.Contains(output, "Accepted findings, outcome since acceptance:") {
		t.Fatalf("no outcome section after acceptance:\n%s", output)
	}
	if !strings.Contains(output, fingerprint+" [") || !strings.Contains(output, "(accepted ") {
		t.Fatalf("accepted finding not reported with its baseline date:\n%s", output)
	}

	listOut := new(bytes.Buffer)
	listCmd := &cobra.Command{}
	listCmd.SetOut(listOut)
	runsFindingsJSON = false
	if err := runRunsFindings(listCmd, nil); err != nil {
		t.Fatalf("findings list: %v", err)
	}
	if !strings.Contains(listOut.String(), "accepted") || !strings.Contains(listOut.String(), "owner/ledger-repo") {
		t.Fatalf("findings list output:\n%s", listOut)
	}

	dismissCmd := &cobra.Command{}
	dismissCmd.SetOut(new(bytes.Buffer))
	if err := setFindingStatus(dismissCmd, fingerprint, ledger.StatusDismissed); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	reloaded, err := ledger.Load(paths.DefaultAdaptiveLedgerPath())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := reloaded.Find(fingerprint)
	if err != nil || entry.Status != ledger.StatusDismissed || entry.AcceptedSnapshot != nil {
		t.Fatalf("dismissed entry = %+v, %v", entry, err)
	}
}
