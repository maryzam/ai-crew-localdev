package telemetry

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	server, payloads := langfusePayloadServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
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
	rec.Finished("passed", exitCode(0), 0, time.Millisecond)
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	startPayload := receivePayload(t, payloads)
	startBatch := payloadBatch(t, startPayload)
	if len(startBatch) != 2 {
		t.Fatalf("start batch length = %d, want trace-create and event-create", len(startBatch))
	}
	if itemType(t, startBatch[0]) != "trace-create" {
		t.Fatalf("first item type = %q, want trace-create", itemType(t, startBatch[0]))
	}
	if itemType(t, startBatch[1]) != "event-create" {
		t.Fatalf("second item type = %q, want event-create", itemType(t, startBatch[1]))
	}

	finishPayload := receivePayload(t, payloads)
	finishBatch := payloadBatch(t, finishPayload)
	if len(finishBatch) != 2 {
		t.Fatalf("finish batch length = %d, want trace-update and event-create", len(finishBatch))
	}
	if itemType(t, finishBatch[0]) != "trace-update" {
		t.Fatalf("first finish item type = %q, want trace-update", itemType(t, finishBatch[0]))
	}
	if itemType(t, finishBatch[1]) != "event-create" {
		t.Fatalf("second finish item type = %q, want event-create", itemType(t, finishBatch[1]))
	}
}

func TestLangfuseIngestionDoesNotBlockRunEvents(t *testing.T) {
	server, _ := langfusePayloadServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	})
	defer server.Close()

	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", filepath.Join(t.TempDir(), "runs.jsonl"))
	t.Setenv("AI_AGENT_LANGFUSE_HOST", server.URL)
	t.Setenv("AI_AGENT_LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("AI_AGENT_LANGFUSE_SECRET_KEY", "sk-test")

	start := time.Now()
	rec, err := StartRun(RunContext{
		RunID:        "run_slow_langfuse",
		AgentName:    "claude",
		Repo:         "owner/repo",
		AgentCommand: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Fatalf("StartRun blocked on Langfuse for %s", elapsed)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestLangfuseIngestionWarnsOnce(t *testing.T) {
	var warnings bytes.Buffer
	origWarnings := langfuseWarnings
	langfuseWarnings = &warnings
	t.Cleanup(func() { langfuseWarnings = origWarnings })

	server, _ := langfusePayloadServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer server.Close()

	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", filepath.Join(t.TempDir(), "runs.jsonl"))
	t.Setenv("AI_AGENT_LANGFUSE_HOST", server.URL)
	t.Setenv("AI_AGENT_LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("AI_AGENT_LANGFUSE_SECRET_KEY", "sk-test")

	rec, err := StartRun(RunContext{
		RunID:        "run_warn_once",
		AgentName:    "claude",
		Repo:         "owner/repo",
		AgentCommand: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	rec.AgentStarted(1)
	rec.Finished("passed", exitCode(0), 0, time.Millisecond)
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := warnings.String()
	if count := strings.Count(got, "warning: langfuse telemetry ingestion failed:"); count != 1 {
		t.Fatalf("warning count = %d, want 1 in %q", count, got)
	}
}

func TestLangfuseCloseStopsAfterFirstDeliveryFailure(t *testing.T) {
	var requests int32
	server, _ := langfusePayloadServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer server.Close()

	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", filepath.Join(t.TempDir(), "runs.jsonl"))
	t.Setenv("AI_AGENT_LANGFUSE_HOST", server.URL)
	t.Setenv("AI_AGENT_LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("AI_AGENT_LANGFUSE_SECRET_KEY", "sk-test")

	rec, err := StartRun(RunContext{
		RunID:        "run_stop_after_failure",
		AgentName:    "claude",
		Repo:         "owner/repo",
		AgentCommand: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	rec.AgentStarted(1)
	rec.AgentFinished(1, "passed", exitCode(0), time.Millisecond)
	rec.VerifyStarted(1, "make verify")
	rec.VerifyFinished(1, "passed", exitCode(0), time.Millisecond)
	rec.Finished("passed", exitCode(0), 0, time.Millisecond)

	start := time.Now()
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Close drained too many failed Langfuse deliveries: %s", elapsed)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("Langfuse requests = %d, want 1 after first failure", got)
	}
}

func TestLangfuseEnqueueAfterCloseIsSafe(t *testing.T) {
	server, _ := langfusePayloadServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	defer server.Close()

	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", filepath.Join(t.TempDir(), "runs.jsonl"))
	t.Setenv("AI_AGENT_LANGFUSE_HOST", server.URL)
	t.Setenv("AI_AGENT_LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("AI_AGENT_LANGFUSE_SECRET_KEY", "sk-test")

	rec, err := StartRun(RunContext{
		RunID:        "run_enqueue_after_close",
		AgentName:    "claude",
		Repo:         "owner/repo",
		AgentCommand: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rec.AgentStarted(1)
}

func TestUnknownModelIsExplicit(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run-telemetry.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")

	rec, err := StartRun(RunContext{
		RunID:        "run_unknown_model",
		AgentName:    "claude",
		Repo:         "owner/repo",
		AgentCommand: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"model":"unknown"`) {
		t.Fatalf("unknown model not recorded explicitly: %s", data)
	}
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
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != "0123456789" {
		t.Fatalf("backup = %q, want original log", backup)
	}
	current, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read current log: %v", err)
	}
	if len(current) != 0 {
		t.Fatalf("current log length = %d, want 0", len(current))
	}
}

func langfusePayloadServer(t *testing.T, respond func(http.ResponseWriter, *http.Request)) (*httptest.Server, chan map[string]any) {
	t.Helper()

	payloads := make(chan map[string]any, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "pk-test" || pass != "sk-test" {
			t.Fatalf("unexpected basic auth user=%q pass=%q ok=%v", user, pass, ok)
		}
		if r.URL.Path != langfuseIngestPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, langfuseIngestPath)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		payloads <- payload
		respond(w, r)
	}))
	return server, payloads
}

func receivePayload(t *testing.T, payloads chan map[string]any) map[string]any {
	t.Helper()
	select {
	case payload := <-payloads:
		return payload
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Langfuse payload")
		return nil
	}
}

func payloadBatch(t *testing.T, payload map[string]any) []any {
	t.Helper()
	batch, ok := payload["batch"].([]any)
	if !ok {
		t.Fatalf("payload batch missing or wrong type: %#v", payload)
	}
	return batch
}

func itemType(t *testing.T, item any) string {
	t.Helper()
	m, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("batch item has wrong type: %#v", item)
	}
	value, ok := m["type"].(string)
	if !ok {
		t.Fatalf("batch item type has wrong type: %#v", item)
	}
	return value
}
