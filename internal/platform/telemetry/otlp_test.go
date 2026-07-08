package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecorderExportsOTLPTraceWithBoundedProjection(t *testing.T) {
	payloads := make(chan map[string]any, 1)
	exporter := exporterFunc(func(data []byte) error {
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		payloads <- payload
		return nil
	})

	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
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
	if err := rec.ConfigureOTLP(exporter); err != nil {
		t.Fatalf("ConfigureOTLP: %v", err)
	}
	rec.SetSessionID("sess-123")
	rec.AgentStarted(1)
	rec.AgentFinished(1, "passed", intPointer(0), time.Millisecond)
	rec.VerifyStarted(1, "tests", "make verify --secret")
	rec.VerifyFinished(1, "tests", "passed", "", intPointer(0), time.Millisecond)
	rec.SessionRevoked()
	rec.Finish(OutcomePassed, PhaseVerify, intPointer(0), 2*time.Millisecond)
	totalTokens := int64(123)
	cost := "0.012300"
	rec.RecordUsage(Usage{Status: "observed", TotalTokens: &totalTokens, CostAmount: &cost, CostCurrency: "USD", Source: "native_otel"})
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
	exporter := exporterFunc(func([]byte) error {
		requests.Add(1)
		time.Sleep(100 * time.Millisecond)
		return errors.New("export failed")
	})

	var warnings bytes.Buffer
	originalWarnings := otlpWarnings
	otlpWarnings = &warnings
	t.Cleanup(func() { otlpWarnings = originalWarnings })
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	rec, err := StartRun(RunContext{RunID: "run_failure", AgentName: "claude", Repo: "owner/repo", HostRepoPath: t.TempDir(), AgentCommand: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.ConfigureOTLP(exporter); err != nil {
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
	sink := &otlpSink{events: make([]Event, 0, 1), warnings: os.Stderr}
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
