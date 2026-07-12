package telemetry

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecorderWritesInspectablePrivacySafeHistory(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run-telemetry.jsonl")
	disableRemoteExport(t)
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)

	rec, err := StartRun(RunContext{
		RunID:         "run_test",
		TaskRef:       "github:owner/repo#43",
		AgentName:     "codex-reviewer",
		Repo:          "owner/repo",
		HostRepoPath:  t.TempDir(),
		AgentCommand:  []string{"codex", "--model", "gpt-5.2-codex", "fix a secret bug"},
		VerifyEnabled: true,
		MaxRetries:    2,
		AuditLogPath:  "/tmp/audit.log",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	rec.SetSessionID("sess-123")
	rec.AgentStarted(1)
	rec.AgentFinished(1, "passed", intPointer(0), time.Millisecond)
	rec.VerifyStarted(1, "tests", "make verify --token=secret")
	rec.VerifyFinished(1, "tests", "passed", "", intPointer(0), time.Millisecond)
	rec.SessionRevoked()
	if !rec.Finish(OutcomePassed, PhaseVerify, intPointer(0), time.Millisecond) {
		t.Fatal("first Finish should record terminal event")
	}
	if rec.Finish(OutcomeVerifyFailed, PhaseVerify, intPointer(1), time.Millisecond) {
		t.Fatal("second Finish should be ignored")
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	raw := string(data)
	for _, sensitive := range []string{"fix a secret bug", "make verify --token=secret"} {
		if strings.Contains(raw, sensitive) {
			t.Fatalf("telemetry leaked %q in %s", sensitive, raw)
		}
	}
	if count := strings.Count(raw, `"event_type":"run.finished"`); count != 1 {
		t.Fatalf("terminal event count = %d, want 1", count)
	}

	runs, err := ReadRunHistory(logPath)
	if err != nil {
		t.Fatalf("ReadRunHistory: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("run count = %d, want 1", len(runs))
	}
	run := runs[0]
	if run.Outcome != OutcomePassed || run.Broker.SessionID != "sess-123" || !run.Broker.SessionRevoked {
		t.Fatalf("unexpected summary: %#v", run)
	}
	if run.Model.Requested != "gpt-5.2-codex" || run.Model.Family != "gpt-5" || run.Model.Provider != "openai" {
		t.Fatalf("unexpected model attribution: %#v", run.Model)
	}
	if run.Task.Ref != "github:owner/repo#43" {
		t.Fatalf("task ref = %q", run.Task.Ref)
	}
	if run.Usage != nil {
		t.Fatalf("unavailable usage should be omitted: %#v", run.Usage)
	}
}

func TestObserveModelStrengthensAttribution(t *testing.T) {
	disableRemoteExport(t)
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", filepath.Join(t.TempDir(), "runs.jsonl"))
	rec, err := StartRun(RunContext{
		RunID:        "run_observed_model",
		AgentName:    "codex",
		Repo:         "owner/repo",
		HostRepoPath: t.TempDir(),
		AgentCommand: []string{"codex", "--model", "gpt-5"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	rec.ObserveModel("gpt-5.2-codex", "openai", "agent_telemetry")
	summary := rec.Summary()
	if summary.Model.Observed != "gpt-5.2-codex" || summary.Model.Resolution.Confidence != "observed" {
		t.Fatalf("model was not strengthened: %#v", summary.Model)
	}
	if !summary.Model.Resolution.Conflict {
		t.Fatal("requested/observed mismatch should be retained as a conflict")
	}
	_ = rec.Close()
}

func TestStartRunUsesPlannedAttributionWhenProvided(t *testing.T) {
	disableRemoteExport(t)
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", filepath.Join(t.TempDir(), "runs.jsonl"))
	rec, err := StartRun(RunContext{
		RunID:        "run_planned_model",
		AgentName:    "codex",
		Agent:        AgentMetadata{Type: "planned_agent", Identity: "planned-identity", Command: "planned-command"},
		Model:        ModelAttribution{Provider: "planned-provider", Family: "planned-family", Requested: "planned-model", Resolution: ModelResolution{Status: "resolved", Confidence: "configured", PrimarySource: "plan", Sources: []string{"plan"}}},
		Repo:         "owner/repo",
		HostRepoPath: t.TempDir(),
		AgentCommand: []string{"codex", "--model", "gpt-5"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	summary := rec.Summary()
	if summary.Agent.Type != "planned_agent" || summary.Agent.Command != "planned-command" || summary.Agent.Identity != "planned-identity" {
		t.Fatalf("planned agent attribution was not used: %#v", summary.Agent)
	}
	if summary.Model.Provider != "planned-provider" || summary.Model.Requested != "planned-model" || summary.Model.Resolution.PrimarySource != "plan" {
		t.Fatalf("planned model attribution was not used: %#v", summary.Model)
	}
	_ = rec.Close()
}

func TestLocalTelemetryRotatesExistingLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run-telemetry.jsonl")
	if err := os.WriteFile(logPath, []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sink, err := newLocalSinkSized(logPath, 8)
	if err != nil {
		t.Fatalf("newLocalSinkSized: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	backup, err := os.ReadFile(logPath + ".1")
	if err != nil || string(backup) != "0123456789" {
		t.Fatalf("rotated backup = %q, err=%v", backup, err)
	}
	info, err := os.Stat(logPath + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("rotated backup mode = %o, want 600", got)
	}
}

func TestLocalTelemetrySerializesConcurrentWritersAndSecuresPermissions(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run-telemetry.jsonl")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	const writers = 32
	sinks := make([]*localSink, writers)
	for index := range sinks {
		sink, err := newLocalSinkSized(logPath, 1024*1024)
		if err != nil {
			t.Fatal(err)
		}
		sinks[index] = sink
	}

	var group sync.WaitGroup
	for index, sink := range sinks {
		group.Add(1)
		go func() {
			defer group.Done()
			event := representativeEvent()
			event.Run.RunID = fmt.Sprintf("run_%032x", index)
			event.Run.TraceID = traceIDForRun(event.Run.RunID)
			if err := sink.Write(event); err != nil {
				t.Errorf("write event: %v", err)
			}
		}()
	}
	group.Wait()
	for _, sink := range sinks {
		if err := sink.Close(); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := ReadRunHistory(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != writers {
		t.Fatalf("runs = %d, want %d", len(runs), writers)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("telemetry mode = %o, want 600", got)
	}
}

func TestLocalTelemetryRejectsSymbolicLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("do not append"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "runs.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := newLocalSink(filepath.Join(dir, "runs.jsonl")); err == nil {
		t.Fatal("symbolic-link telemetry path accepted")
	}
}

func TestRecorderReportsLocalWriteFailure(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "runs.jsonl")
	disableRemoteExport(t)
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)

	var warnings bytes.Buffer
	originalWarnings := localWarnings
	localWarnings = &warnings
	t.Cleanup(func() { localWarnings = originalWarnings })

	recorder, err := StartRun(RunContext{RunID: "run_write_failure", AgentName: "codex", HostRepoPath: dir, AgentCommand: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(logPath, 0o700); err != nil {
		t.Fatal(err)
	}
	recorder.AgentStarted(1)
	if err := recorder.Close(); err == nil {
		t.Fatal("local write failure was not returned by Close")
	}
	if count := strings.Count(warnings.String(), "warning: local managed-run telemetry failed:"); count != 1 {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func disableRemoteExport(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AI_AGENT_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT",
		"AI_AGENT_LANGFUSE_PUBLIC_KEY", "LANGFUSE_PUBLIC_KEY",
		"AI_AGENT_LANGFUSE_SECRET_KEY", "LANGFUSE_SECRET_KEY",
	} {
		t.Setenv(key, "")
	}
}

func intPointer(value int) *int {
	return &value
}

func TestVerifyFinishedAccumulatesContractResults(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	rec, err := StartRun(RunContext{RunID: "run_contracts", AgentName: "claude", Repo: "o/r"})
	if err != nil {
		t.Fatal(err)
	}

	rec.VerifyStarted(1, "tests", "make test")
	rec.VerifyFinished(1, "tests", "failed", "exit", intPointer(2), time.Millisecond)
	rec.VerifyStarted(2, "tests", "make test")
	rec.VerifyFinished(2, "tests", "passed", "", intPointer(0), time.Millisecond)
	rec.VerifyStarted(2, "lint", "make lint")
	rec.VerifyFinished(2, "lint", "passed", "", intPointer(0), time.Millisecond)
	rec.Finish(OutcomePassed, PhaseVerify, intPointer(0), 0)
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	runs, err := ReadRunHistory(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	contracts := runs[0].Verification.Contracts
	if len(contracts) != 2 {
		t.Fatalf("contracts = %+v, want tests and lint", contracts)
	}
	if contracts[0].Name != "tests" || contracts[0].Outcome != "passed" || contracts[0].Attempts != 2 || contracts[0].FailureClass != "" {
		t.Fatalf("tests contract = %+v, want passed after 2 attempts with cleared failure class", contracts[0])
	}
	if contracts[1].Name != "lint" || contracts[1].Outcome != "passed" || contracts[1].Attempts != 1 {
		t.Fatalf("lint contract = %+v", contracts[1])
	}
}

func TestContractResultsKeepDistinctLongNames(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	rec, err := StartRun(RunContext{RunID: "run_longnames", AgentName: "claude", Repo: "o/r"})
	if err != nil {
		t.Fatal(err)
	}

	shared := strings.Repeat("x", MaxPropagatedValueLength)
	first := shared + "-alpha"
	second := shared + "-beta"
	rec.VerifyFinished(1, first, "passed", "", intPointer(0), time.Millisecond)
	rec.VerifyFinished(1, second, "failed", "exit", intPointer(1), time.Millisecond)
	rec.Finish(OutcomeVerifyFailed, PhaseVerify, intPointer(1), 0)
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	runs, err := ReadRunHistory(logPath)
	if err != nil {
		t.Fatal(err)
	}
	contracts := runs[0].Verification.Contracts
	if len(contracts) != 2 {
		t.Fatalf("contracts = %+v; distinct names beyond the export bound must not merge", contracts)
	}
	if contracts[0].Name != first || contracts[1].Name != second {
		t.Fatalf("contract names = %q, %q; want full local names", contracts[0].Name, contracts[1].Name)
	}
}
