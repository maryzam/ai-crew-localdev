package langfuse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
)

const validOTLPPayload = `{"resourceSpans":[{"resource":{},"scopeSpans":[{"scope":{"name":"ai-agent-native"},"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"agent.operation","startTimeUnixNano":"1","endTimeUnixNano":"2"}]}]}]}`

func TestProviderPublishesWithDurableSecretOnlyInUpstreamAuthorization(t *testing.T) {
	var authorization string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		if request.URL.Path != "/v1/traces" {
			t.Errorf("path = %q", request.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	provider, config := configuredProvider(t, backend.URL, 0o600)
	err := provider.PublishTelemetry(context.Background(), port.ProviderTelemetryRequest{
		Resource: api.ResourceURI{Provider: "langfuse", Kind: "project", Identifier: "managed-runs"},
		Config:   config,
		Payload:  json.RawMessage(validOTLPPayload),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("pk-test:sk-durable-test"))
	if authorization != want {
		t.Fatalf("authorization = %q", authorization)
	}
}

func TestProviderRejectsInsecureCredentialFile(t *testing.T) {
	provider, config := configuredProvider(t, "http://example.test", 0o644)
	err := provider.PublishTelemetry(context.Background(), port.ProviderTelemetryRequest{
		Resource: api.ResourceURI{Provider: "langfuse", Kind: "project", Identifier: "managed-runs"},
		Config:   config,
		Payload:  json.RawMessage(validOTLPPayload),
	})
	if err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderRejectsProjectMismatchBeforeEgress(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer backend.Close()
	provider, config := configuredProvider(t, backend.URL, 0o600)
	err := provider.PublishTelemetry(context.Background(), port.ProviderTelemetryRequest{
		Resource: api.ResourceURI{Provider: "langfuse", Kind: "project", Identifier: "other"},
		Config:   config,
		Payload:  json.RawMessage(validOTLPPayload),
	})
	if err == nil || requests.Load() != 0 {
		t.Fatalf("error = %v, requests = %d", err, requests.Load())
	}
}

func TestProviderRejectsCredentialFileSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "credentials.env")
	if err := os.WriteFile(target, credentialData(), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "credentials-link.env")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	provider := New()
	config, err := provider.ParseConfig("codex", configJSON(t, link, "http://example.test"))
	if err != nil {
		t.Fatal(err)
	}
	err = provider.PublishTelemetry(context.Background(), port.ProviderTelemetryRequest{
		Resource: api.ResourceURI{Provider: "langfuse", Kind: "project", Identifier: "managed-runs"},
		Config:   config,
		Payload:  json.RawMessage(validOTLPPayload),
	})
	if err == nil {
		t.Fatal("credential file symlink accepted")
	}
}

func TestProviderRejectsUnapprovedTelemetryBeforeEgress(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer backend.Close()
	provider, config := configuredProvider(t, backend.URL, 0o600)
	payload := strings.Replace(validOTLPPayload, `"name":"agent.operation"`, `"name":"agent.operation","attributes":[{"key":"user_prompt","value":{"stringValue":"private"}}]`, 1)
	err := provider.PublishTelemetry(context.Background(), port.ProviderTelemetryRequest{
		Resource: api.ResourceURI{Provider: "langfuse", Kind: "project", Identifier: "managed-runs"},
		Config:   config,
		Payload:  json.RawMessage(payload),
	})
	if err == nil || !strings.Contains(err.Error(), "user_prompt") || requests.Load() != 0 {
		t.Fatalf("error = %v, requests = %d", err, requests.Load())
	}
}

func TestTraceEndpointMapsContainerHostToBrokerLoopback(t *testing.T) {
	got := traceEndpoint("http://host.containers.internal:3000/api/public/otel")
	if got != "http://127.0.0.1:3000/api/public/otel/v1/traces" {
		t.Fatalf("endpoint = %q", got)
	}
}

func TestBrokerAndProviderEnforceSameTelemetryPayloadBudget(t *testing.T) {
	if api.MaxTelemetryPayloadBytes != telemetry.MaxOTLPExportPayloadBytes {
		t.Fatalf("broker budget = %d, provider budget = %d", api.MaxTelemetryPayloadBytes, telemetry.MaxOTLPExportPayloadBytes)
	}
}

func TestProviderEgressHonorsRequestDeadline(t *testing.T) {
	original := newHTTPClient
	newHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})}
	}
	t.Cleanup(func() { newHTTPClient = original })
	provider, config := configuredProvider(t, "http://example.test", 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := provider.PublishTelemetry(ctx, port.ProviderTelemetryRequest{
		Resource: api.ResourceURI{Provider: "langfuse", Kind: "project", Identifier: "managed-runs"}, Config: config, Payload: json.RawMessage(validOTLPPayload),
	})
	if err == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("error = %v, elapsed = %s", err, time.Since(start))
	}
}

func configuredProvider(t *testing.T, endpoint string, mode os.FileMode) (*Provider, any) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, credentialData(), mode); err != nil {
		t.Fatal(err)
	}
	provider := New()
	config, err := provider.ParseConfig("codex", configJSON(t, path, endpoint))
	if err != nil {
		t.Fatal(err)
	}
	return provider, config
}

func credentialData() []byte {
	return []byte("LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk-test\nLANGFUSE_INIT_PROJECT_SECRET_KEY='sk-durable-test'\n")
}

func configJSON(t *testing.T, path, endpoint string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(rawConfig{CredentialsFile: path, Endpoint: endpoint, Project: "managed-runs"})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
