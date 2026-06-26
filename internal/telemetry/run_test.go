package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecorderWritesLocalRunHistoryWithoutPromptArgs(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run-telemetry.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")

	rec, err := StartRun(RunContext{
		RunID:         "run_test",
		AgentName:     "codex",
		Repo:          "owner/repo",
		HostRepoPath:  "/workspace/repo",
		AgentCommand:  []string{"codex", "--model", "gpt-5", "fix a secret bug"},
		VerifyEnabled: true,
		AuditLogPath:  "/tmp/audit.log",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	rec.SetSessionID("sess-123")
	rec.AgentStarted(1)
	rec.AgentFinished(1, "passed", exitCode(0), time.Millisecond)
	rec.VerifyStarted(1, "make verify --token=secret")
	rec.VerifyFinished(1, "passed", exitCode(0), time.Millisecond)
	rec.UsageUnknown()
	rec.Finished("passed", exitCode(0), 0, time.Millisecond)
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
			t.Fatalf("telemetry leaked full command text %q in %s", sensitive, raw)
		}
	}
	if !strings.Contains(raw, `"event_type":"usage.recorded"`) {
		t.Fatalf("usage event missing from telemetry: %s", raw)
	}
	if !strings.Contains(raw, `"input_tokens":"unknown"`) || !strings.Contains(raw, `"cost_usd":"unknown"`) {
		t.Fatalf("usage unknown fields missing from telemetry: %s", raw)
	}
	if !strings.Contains(raw, `"session_id":"sess-123"`) {
		t.Fatalf("session id not correlated in telemetry: %s", raw)
	}
	if !strings.Contains(raw, `"model":"gpt-5"`) {
		t.Fatalf("model was not inferred from command args: %s", raw)
	}
}

func exitCode(v int) *int {
	return &v
}

func TestRecorderMirrorsStartEventToLangfuse(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "pk-test" || pass != "sk-test" {
			t.Fatalf("unexpected basic auth user=%q pass=%q ok=%v", user, pass, ok)
		}
		if r.URL.Path != langfuseIngestPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, langfuseIngestPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", filepath.Join(t.TempDir(), "runs.jsonl"))
	t.Setenv("AI_AGENT_LANGFUSE_HOST", server.URL)
	t.Setenv("AI_AGENT_LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("AI_AGENT_LANGFUSE_SECRET_KEY", "sk-test")

	rec, err := StartRun(RunContext{
		RunID:        "run_langfuse",
		AgentName:    "claude",
		Repo:         "owner/repo",
		AgentCommand: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer func() { _ = rec.Close() }()

	batch, ok := payload["batch"].([]any)
	if !ok {
		t.Fatalf("payload batch missing or wrong type: %#v", payload)
	}
	if len(batch) != 2 {
		t.Fatalf("batch length = %d, want trace-create and event-create", len(batch))
	}
	trace, ok := batch[0].(map[string]any)
	if !ok || trace["type"] != "trace-create" {
		t.Fatalf("first item = %#v, want trace-create", batch[0])
	}
	event, ok := batch[1].(map[string]any)
	if !ok || event["type"] != "event-create" {
		t.Fatalf("second item = %#v, want event-create", batch[1])
	}
}
