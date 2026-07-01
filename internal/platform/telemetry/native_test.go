package telemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeRelayCollectsUsageAndSanitizesTraces(t *testing.T) {
	payloads := make(chan []byte, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		user, password, ok := request.BasicAuth()
		if !ok || user != "pk-test" || password != "sk-test" {
			t.Errorf("backend auth = %q %q %t", user, password, ok)
		}
		data, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		payloads <- data
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_native", AgentName: "claude", Repo: "owner/repo", AgentCommand: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := StartNativeRelay(recorder, OTLPConfig{Endpoint: backend.URL, PublicKey: "pk-test", SecretKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}

	logs := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"claude_code.api_request"},"attributes":[{"key":"model","value":{"stringValue":"claude-sonnet-4-6"}},{"key":"input_tokens","value":{"intValue":"100"}},{"key":"output_tokens","value":{"intValue":"20"}},{"key":"cache_read_tokens","value":{"intValue":"30"}},{"key":"cache_creation_tokens","value":{"intValue":"5"}},{"key":"cost_usd","value":{"doubleValue":0.25}}]}]}]}]}`
	postNativeSignal(t, relay, "/v1/logs", logs)

	traces := `{"secret":"top-level-secret","resourceSpans":[{"resource":{"attributes":[{"key":"user.email","value":{"stringValue":"secret@example.test"}}]},"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"claude_code.llm_request","secret":"span-secret","attributes":[{"key":"user_prompt","value":{"stringValue":"private prompt"}},{"key":"input_tokens","value":{"intValue":"100"}}]}]}]}]}`
	postNativeSignal(t, relay, "/v1/traces", traces)

	recorder.Finish(OutcomePassed, PhaseAgent, intPointer(0), 0)
	relay.Close()
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

	var forwarded string
	for range 2 {
		select {
		case payload := <-payloads:
			text := string(payload)
			if strings.Contains(text, "claude_code.llm_request") {
				forwarded = text
			}
		default:
		}
	}
	if forwarded == "" {
		t.Fatal("native trace was not forwarded")
	}
	for _, forbidden := range []string{"top-level-secret", "span-secret", "secret@example.test", "private prompt", "user.email", "user_prompt"} {
		if strings.Contains(forwarded, forbidden) {
			t.Errorf("forwarded trace leaked %q: %s", forbidden, forwarded)
		}
	}
	for _, required := range []string{"ai_agent.run.id", "run_native", "gen_ai.usage.input_tokens"} {
		if !strings.Contains(forwarded, required) {
			t.Errorf("forwarded trace missing %q: %s", required, forwarded)
		}
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
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer backend.Close()
	relay, err := StartNativeRelay(recorder, OTLPConfig{Endpoint: backend.URL})
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
	relay.Close()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeFieldPolicyRejectsUnknownAndSensitiveAttributes(t *testing.T) {
	attributes := []otlpWireAttribute{
		newOTLPWireAttribute("input_tokens", int64(12)),
		newOTLPWireAttribute("ai_agent.repository.root_path", "/private/repo"),
		newOTLPWireAttribute("user.email", "person@example.test"),
	}
	result := sanitizeNativeAttributes(attributes, RunSummary{}, false)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, "gen_ai.usage.input_tokens") {
		t.Fatalf("canonical native field missing: %s", text)
	}
	for _, rejected := range []string{"ai_agent.repository.root_path", "user.email", "/private/repo", "person@example.test"} {
		if strings.Contains(text, rejected) {
			t.Errorf("native attribute policy leaked %q: %s", rejected, text)
		}
	}
}

func TestNativeAttributeAliasesResolveToExportPolicies(t *testing.T) {
	for alias := range nativeAttributeAliases {
		field, policy, ok := nativeField(alias)
		if !ok {
			t.Errorf("native alias %q has no export policy", alias)
			continue
		}
		if policy.Sensitive || !policy.NativeInput || !fieldAllowed(field, "otlp") {
			t.Errorf("native alias %q resolves to invalid policy %#v", alias, policy)
		}
	}
}
