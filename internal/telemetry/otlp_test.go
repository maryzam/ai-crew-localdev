package telemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecorderExportsOTLPTraceWithBoundedProjection(t *testing.T) {
	payloads := make(chan map[string]any, 1)
	originalClient := newOTLPHTTPClient
	newOTLPHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/api/public/otel/v1/traces" {
				t.Errorf("path = %q", request.URL.Path)
			}
			user, pass, ok := request.BasicAuth()
			if !ok || user != "pk-test" || pass != "sk-test" {
				t.Errorf("basic auth user=%q pass=%q ok=%t", user, pass, ok)
			}
			if request.Header.Get("Content-Type") != "application/json" {
				t.Errorf("content type = %q", request.Header.Get("Content-Type"))
			}
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode payload: %v", err)
			}
			payloads <- payload
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		})}
	}
	t.Cleanup(func() { newOTLPHTTPClient = originalClient })

	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	t.Setenv("AI_AGENT_LANGFUSE_HOST", "http://example.test")
	t.Setenv("AI_AGENT_LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("AI_AGENT_LANGFUSE_SECRET_KEY", "sk-test")
	t.Setenv("AI_AGENT_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	rec, err := StartRun(RunContext{
		RunID:         "run_otlp",
		TaskRef:       "github:owner/repo#43",
		AgentName:     "codex",
		Repo:          "owner/repo",
		HostRepoPath:  "/private/workspace/repo",
		AgentCommand:  []string{"codex", "--model", "gpt-5", "private prompt"},
		VerifyEnabled: true,
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	rec.SetSessionID("sess-123")
	rec.AgentStarted(1)
	rec.AgentFinished(1, "passed", intPointer(0), time.Millisecond)
	rec.VerifyStarted(1, "make verify --secret")
	rec.VerifyFinished(1, "passed", intPointer(0), time.Millisecond)
	rec.SessionRevoked()
	rec.Finish(OutcomePassed, PhaseVerify, intPointer(0), 2*time.Millisecond)
	totalTokens := int64(123)
	cost := "0.012300"
	rec.RecordUsage(Usage{Status: "estimated", TotalTokens: &totalTokens, CostAmount: &cost, CostCurrency: "USD", Source: "ccusage_delta"})
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	payload := <-payloads
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(encoded)
	for _, required := range []string{"ai-agent-launcher", "ai_agent.run", "agent.command", "verify.attempt", "gen_ai.request.model", "gen_ai.usage.total_tokens", "ai_agent.usage.cost.amount", "langfuse.session.id", "github:owner/repo#43"} {
		if !strings.Contains(raw, required) {
			t.Errorf("OTLP payload missing %q: %s", required, raw)
		}
	}
	for _, forbidden := range []string{"/private/workspace/repo", "private prompt", "make verify --secret"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("OTLP payload leaked %q", forbidden)
		}
	}
}

func TestOTLPCloseIsBoundedAfterFailure(t *testing.T) {
	var requests atomic.Int32
	originalClient := newOTLPHTTPClient
	newOTLPHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			requests.Add(1)
			time.Sleep(100 * time.Millisecond)
			return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		})}
	}
	t.Cleanup(func() { newOTLPHTTPClient = originalClient })

	var warnings bytes.Buffer
	originalWarnings := otlpWarnings
	otlpWarnings = &warnings
	t.Cleanup(func() { otlpWarnings = originalWarnings })
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	t.Setenv("AI_AGENT_OTLP_TRACES_ENDPOINT", "http://example.test")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("AI_AGENT_LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("AI_AGENT_LANGFUSE_SECRET_KEY", "")

	rec, err := StartRun(RunContext{RunID: "run_failure", AgentName: "claude", Repo: "owner/repo", HostRepoPath: t.TempDir(), AgentCommand: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 50; attempt++ {
		rec.AgentStarted(attempt)
		rec.AgentFinished(attempt, "failed", intPointer(1), time.Millisecond)
	}
	rec.Finish(OutcomeAgentFailed, PhaseAgent, intPointer(1), time.Millisecond)
	start := time.Now()
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Close took %s", elapsed)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
	if strings.Count(warnings.String(), "warning: OTLP telemetry export failed:") != 1 {
		t.Fatalf("warnings = %q", warnings.String())
	}
	runs, err := ReadRunHistory(logPath)
	if err != nil || len(runs) != 1 || runs[0].Outcome != OutcomeAgentFailed {
		t.Fatalf("local history after exporter failure = %#v, err=%v", runs, err)
	}
}

func TestOTLPEnqueueAfterCloseIsSafe(t *testing.T) {
	sink := &otlpSink{events: make([]Event, 0, 1), client: http.DefaultClient, warnings: os.Stderr}
	sink.close()
	sink.enqueue(representativeEvent())
	if len(sink.events) != 0 {
		t.Fatalf("events after close = %d", len(sink.events))
	}
}

func TestOTLPQueuePreservesTerminalEvent(t *testing.T) {
	sink := &otlpSink{events: make([]Event, 0, otlpQueueSize), warnings: io.Discard}
	for range otlpQueueSize {
		event := representativeEvent()
		event.EventType = "agent.command.started"
		sink.enqueue(event)
	}
	terminal := representativeEvent()
	terminal.EventType = "run.finished"
	terminal.Outcome = OutcomePassed
	sink.enqueue(terminal)
	if got := sink.events[len(sink.events)-1]; got.EventType != "run.finished" || got.Outcome != OutcomePassed {
		t.Fatalf("last queued event = %#v", got)
	}
}

func TestOTLPEndpointEnvironmentSemantics(t *testing.T) {
	for _, key := range []string{"AI_AGENT_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		t.Setenv(key, "")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://collector.example/custom-traces")
	if got := traceEndpointFromEnv(); got != "https://collector.example/custom-traces" {
		t.Fatalf("signal endpoint = %q", got)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://collector.example/base")
	if got := traceEndpointFromEnv(); got != "https://collector.example/base/v1/traces" {
		t.Fatalf("base endpoint = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
