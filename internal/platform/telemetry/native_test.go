package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/runevents"
)

type nativeExporterFunc func(string, []byte) error

func (f nativeExporterFunc) Export(_ context.Context, signal string, payload []byte) error {
	return f(signal, payload)
}

type recordedNativeExport struct {
	signal  string
	payload []byte
}

func TestNativeRelayCollectsUsageAndForwardsRawSignals(t *testing.T) {
	payloads := make(chan recordedNativeExport, 2)
	exporter := nativeExporterFunc(func(signal string, payload []byte) error {
		payloads <- recordedNativeExport{signal: signal, payload: append([]byte(nil), payload...)}
		return nil
	})

	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_native", AgentName: "claude", Repo: "owner/repo", AgentCommand: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := StartNativeRelay(recorder, exporter)
	if err != nil {
		t.Fatal(err)
	}

	logs := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"claude_code.api_request"},"attributes":[{"key":"model","value":{"stringValue":"claude-sonnet-4-6"}},{"key":"input_tokens","value":{"intValue":"100"}},{"key":"output_tokens","value":{"intValue":"20"}},{"key":"cache_read_tokens","value":{"intValue":"30"}},{"key":"cache_creation_tokens","value":{"intValue":"5"}},{"key":"cost_usd","value":{"doubleValue":0.25}}]}]}]}]}`
	postNativeSignal(t, relay, "/v1/logs", logs)

	traces := `{"secret":"top-level-secret","resourceSpans":[{"resource":{"attributes":[{"key":"user.email","value":{"stringValue":"secret@example.test"}}]},"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"claude_code.llm_request","startTimeUnixNano":"1","endTimeUnixNano":"2","secret":"span-secret","attributes":[{"key":"user_prompt","value":{"stringValue":"private prompt"}},{"key":"input_tokens","value":{"intValue":"100"}}]}]}]}]}`
	postNativeSignal(t, relay, "/v1/traces", traces)

	recorder.Finish(OutcomePassed, PhaseAgent, intPointer(0), 0)
	stats := relay.Close()
	if stats.Rejected != 0 || stats.Failed != 0 {
		t.Fatalf("relay stats = %+v", stats)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	runs, err := ReadRunHistory(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Usage == nil || runs[0].Usage.TotalTokens == nil || *runs[0].Usage.TotalTokens != 155 {
		t.Fatalf("runs = %#v", runs)
	}
	if runs[0].Usage.Source != "native_otel" || runs[0].Usage.Confidence != "provider_reported" {
		t.Fatalf("usage attribution = %#v", runs[0].Usage)
	}

	forwarded := map[string]string{}
	for range 2 {
		select {
		case item := <-payloads:
			forwarded[item.signal] = string(item.payload)
		default:
		}
	}
	if forwarded["logs"] != logs || forwarded["traces"] != traces {
		t.Fatalf("forwarded signals = %#v", forwarded)
	}
}

func TestNativeRelayAcknowledgesWithoutWaitingForStorageAndDeliversBeforeClose(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_async", AgentName: "codex", Repo: "owner/repo", AgentCommand: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var stored atomic.Int32
	relay, err := StartNativeRelay(recorder, nativeExporterFunc(func(string, []byte) error {
		<-release
		stored.Add(1)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	postNativeSignal(t, relay, "/v1/traces", `{"resourceSpans":[]}`)
	postNativeSignal(t, relay, "/v1/traces", `{"resourceSpans":[]}`)
	close(release)
	stats := relay.Close()
	if stats.Rejected != 0 || stats.Failed != 0 || stored.Load() != 2 {
		t.Fatalf("relay stats = %+v stored = %d", stats, stored.Load())
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeRelayEnforcesUsageWhenStorageFailsAndMeasuresTheLoss(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_store_failure", AgentName: "codex", Repo: "owner/repo", AgentCommand: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	var observed atomic.Int64
	relay, err := StartNativeRelay(recorder, nativeExporterFunc(func(string, []byte) error { return errors.New("storage unavailable") }), WithNativeUsageObserver(func(usage runevents.NativeUsage) { observed.Add(usage.Total) }))
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"codex.sse_event"},"attributes":[{"key":"event.kind","value":{"stringValue":"response.completed"}},{"key":"input_token_count","value":{"intValue":"7"}}]}]}]}]}`
	postNativeSignal(t, relay, "/v1/logs", payload)
	if observed.Load() != 7 {
		t.Fatalf("budget observer saw %d tokens before storage", observed.Load())
	}
	stats := relay.Close()
	if stats.Rejected != 0 || stats.Failed != 1 {
		t.Fatalf("relay stats = %+v", stats)
	}
	if usage := recorder.Summary().Usage; usage == nil || usage.TotalTokens == nil || *usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v", usage)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeRelayDrainBudgetBoundsCloseAndCountsUndeliveredSignals(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_hung_broker", AgentName: "codex", Repo: "owner/repo", AgentCommand: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := StartNativeRelay(recorder, hungExporter{})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		postNativeSignal(t, relay, "/v1/traces", `{"resourceSpans":[]}`)
	}
	start := time.Now()
	stats := relay.Close()
	if elapsed := time.Since(start); elapsed > nativeDrainBudget+nativeShutdownBudget {
		t.Fatalf("close took %s", elapsed)
	}
	if stats.Failed != 3 {
		t.Fatalf("relay stats = %+v", stats)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

type hungExporter struct{}

func (hungExporter) Export(ctx context.Context, _ string, _ []byte) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestNativeRelayCollectsCodexUsageWithoutRemoteExporter(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_codex_local", AgentName: "codex", Repo: "owner/repo", AgentCommand: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	relay := &NativeRelay{recorder: recorder, token: "local-token"}

	logs := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"codex.sse_event"},"attributes":[{"key":"event.kind","value":{"stringValue":"response.completed"}},{"key":"model","value":{"stringValue":"gpt-5-codex"}},{"key":"input_token_count","value":{"intValue":"80"}},{"key":"output_token_count","value":{"intValue":"20"}},{"key":"cached_token_count","value":{"intValue":"30"}},{"key":"reasoning_token_count","value":{"intValue":"10"}}]}]}]}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewBufferString(logs))
	request.Header.Set("Authorization", "Bearer local-token")
	response := httptest.NewRecorder()
	relay.handleLogs(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}

	recorder.Finish(OutcomePassed, PhaseAgent, intPointer(0), 0)
	relay.recordUsage()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	runs, err := ReadRunHistory(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Usage == nil {
		t.Fatalf("runs = %#v", runs)
	}
	usage := runs[0].Usage
	if usage.TotalTokens == nil || *usage.TotalTokens != 100 || usage.CacheReadTokens == nil || *usage.CacheReadTokens != 30 || usage.ReasoningTokens == nil || *usage.ReasoningTokens != 10 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.CostAmount != nil || usage.CostCurrency != "" {
		t.Fatalf("unsupported Codex cost must remain empty: %#v", usage)
	}
	if usage.Source != "native_otel" || usage.Scope != "run" || usage.Precision != "request" || usage.Confidence != "provider_reported" {
		t.Fatalf("usage attribution = %#v", usage)
	}
}

func TestNativeRelayDoesNotAttributeMixedModelUsageToLastModel(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_mixed_model_usage", AgentName: "codex", Repo: "owner/repo", AgentCommand: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	relay := &NativeRelay{recorder: recorder, token: "local-token"}

	first := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"codex.sse_event"},"attributes":[{"key":"event.kind","value":{"stringValue":"response.completed"}},{"key":"model","value":{"stringValue":"gpt-test-a"}},{"key":"input_token_count","value":{"intValue":"80"}},{"key":"output_token_count","value":{"intValue":"20"}}]}]}]}]}`
	second := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"codex.sse_event"},"attributes":[{"key":"event.kind","value":{"stringValue":"response.completed"}},{"key":"model","value":{"stringValue":"gpt-test-b"}},{"key":"input_token_count","value":{"intValue":"10"}},{"key":"output_token_count","value":{"intValue":"5"}}]}]}]}]}`
	postNativeRelayRequest(t, relay, first)
	postNativeRelayRequest(t, relay, second)

	recorder.Finish(OutcomePassed, PhaseAgent, intPointer(0), 0)
	relay.recordUsage()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	runs, err := ReadRunHistory(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Usage == nil || runs[0].Usage.TotalTokens == nil || *runs[0].Usage.TotalTokens != 115 {
		t.Fatalf("runs = %#v", runs)
	}
	if runs[0].Model.Observed == "gpt-test-b" || runs[0].Model.Observed == "gpt-test-a" {
		t.Fatalf("mixed model usage attributed to one model: %#v", runs[0].Model)
	}
}

func TestNativeRelayPublishesNativeUsageToObserver(t *testing.T) {
	recorder, err := StartRun(RunContext{RunID: "run_native_observer", AgentName: "claude", Repo: "owner/repo", AgentCommand: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := recorder.Close(); err != nil {
			t.Fatal(err)
		}
	})
	var observed []runevents.NativeUsage
	relay := &NativeRelay{
		recorder: recorder,
		token:    "local-token",
		observer: func(usage runevents.NativeUsage) {
			observed = append(observed, usage)
		},
	}

	postNativeRelayRequest(t, relay, `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"claude_code.api_request"},"attributes":[{"key":"model","value":{"stringValue":"claude-test"}},{"key":"input_tokens","value":{"intValue":"40"}},{"key":"output_tokens","value":{"intValue":"6"}},{"key":"cost_usd","value":{"doubleValue":0.02}}]}]}]}]}`)

	if len(observed) != 1 {
		t.Fatalf("observed usage count = %d, want 1", len(observed))
	}
	if observed[0].Total != 46 || observed[0].CostUSD != 0.02 || observed[0].Model != "claude-test" {
		t.Fatalf("observed usage = %#v", observed[0])
	}
}

func postNativeRelayRequest(t *testing.T, relay *NativeRelay, payload string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewBufferString(payload))
	request.Header.Set("Authorization", "Bearer local-token")
	response := httptest.NewRecorder()
	relay.handleLogs(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func postNativeSignal(t *testing.T, relay *NativeRelay, path, payload string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, relay.Endpoint()+path, bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", relay.Authorization())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		var body any
		_ = json.NewDecoder(response.Body).Decode(&body)
		t.Fatalf("status = %d body=%v", response.StatusCode, body)
	}
}

func TestNativeRelayRejectsMissingAuthorization(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_auth", AgentName: "codex", Repo: "owner/repo", AgentCommand: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := StartNativeRelay(recorder, nativeExporterFunc(func(string, []byte) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, relay.Endpoint()+"/v1/traces", bytes.NewBufferString(`{"resourceSpans":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.StatusCode)
	}
	stats := relay.Close()
	if stats.Rejected != 1 || stats.Failed != 0 {
		t.Fatalf("relay stats = %+v", stats)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeRelayPreservesUnknownTraceFieldsForHostStorage(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_malformed", AgentName: "codex", Repo: "owner/repo", AgentCommand: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	var exports atomic.Int32
	relay, err := StartNativeRelay(recorder, nativeExporterFunc(func(_ string, _ []byte) error {
		exports.Add(1)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, relay.Endpoint()+"/v1/traces", bytes.NewBufferString(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"invalid","spanId":"invalid","name":"gen_ai.request","startTimeUnixNano":"1","endTimeUnixNano":"2"}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", relay.Authorization())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	relay.Close()
	if exports.Load() != 1 {
		t.Fatalf("raw trace exports = %d", exports.Load())
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}
