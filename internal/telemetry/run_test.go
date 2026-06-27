package telemetry

import (
	"os"
	"path/filepath"
	"strings"
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
	rec.VerifyStarted(1, "make verify --token=secret")
	rec.VerifyFinished(1, "passed", intPointer(0), time.Millisecond)
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

func TestLocalTelemetryRotatesExistingLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run-telemetry.jsonl")
	if err := os.WriteFile(logPath, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sink, err := newLocalSinkSized(logPath, 8)
	if err != nil {
		t.Fatalf("newLocalSinkSized: %v", err)
	}
	if err := sink.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	backup, err := os.ReadFile(logPath + ".1")
	if err != nil || string(backup) != "0123456789" {
		t.Fatalf("rotated backup = %q, err=%v", backup, err)
	}
}

func disableRemoteExport(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AI_AGENT_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"AI_AGENT_LANGFUSE_PUBLIC_KEY", "LANGFUSE_PUBLIC_KEY",
		"AI_AGENT_LANGFUSE_SECRET_KEY", "LANGFUSE_SECRET_KEY",
	} {
		t.Setenv(key, "")
	}
}

func intPointer(value int) *int {
	return &value
}
