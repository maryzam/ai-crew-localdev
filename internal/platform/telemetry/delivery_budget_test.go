package telemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func saturatedFinishedEvents() []Event {
	events := make([]Event, 0, otlpQueueSize)
	for range otlpQueueSize {
		event := representativeEvent()
		event.EventType = "agent.command.finished"
		events = append(events, event)
	}
	return events
}

func TestOTLPPayloadStaysWithinByteBudget(t *testing.T) {
	payload, err := buildOTLPPayload(saturatedFinishedEvents())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("payload is empty")
	}
	if len(data) > maxOTLPPayloadBytes {
		t.Fatalf("full-queue payload = %d bytes, budget %d", len(data), maxOTLPPayloadBytes)
	}
}

func TestOTLPExportRejectsOverBudgetPayloadWithoutSending(t *testing.T) {
	original := maxOTLPPayloadBytes
	maxOTLPPayloadBytes = 256
	t.Cleanup(func() { maxOTLPPayloadBytes = original })

	var requests atomic.Int32
	sink := &otlpSink{
		warnings: io.Discard,
		exporter: exporterFunc(func([]byte) error {
			requests.Add(1)
			return nil
		}),
	}

	err := sink.ingest(saturatedFinishedEvents())
	if err == nil || !strings.Contains(err.Error(), "exceeds budget") {
		t.Fatalf("ingest error = %v, want budget rejection", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("over-budget payload was sent %d times", requests.Load())
	}
}

func TestNativeRelayRejectsOversizedPayload(t *testing.T) {
	logPath := t.TempDir() + "/runs.jsonl"
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_oversized", AgentName: "claude", Repo: "owner/repo", AgentCommand: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := StartNativeRelay(recorder, exporterFunc(func([]byte) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		relay.Close()
		_ = recorder.Close()
	})

	oversized := bytes.Repeat([]byte("a"), nativeRequestLimit+1)
	request, err := http.NewRequest(http.MethodPost, relay.Endpoint()+"/v1/traces", bytes.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", relay.Authorization())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusRequestEntityTooLarge)
	}
}

func BenchmarkBuildOTLPPayload(b *testing.B) {
	events := saturatedFinishedEvents()
	b.ResetTimer()
	for b.Loop() {
		payload, err := buildOTLPPayload(events)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := json.Marshal(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func representativeEventFinished() Event {
	event := representativeEvent()
	event.EventType = "agent.command.finished"
	return event
}
