package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
	langfuseprovider "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse"
)

const brokerTestOTLPPayload = `{"resourceSpans":[{"resource":{},"scopeSpans":[{"scope":{"name":"ai-agent-native"},"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"agent.operation","startTimeUnixNano":"1","endTimeUnixNano":"2"}]}]}]}`

func testLangfuseBroker(t *testing.T, endpoint string) (*Broker, string, func()) {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "broker.sock")
	auditPath := filepath.Join(dir, "audit.log")
	credentialsPath := filepath.Join(dir, "langfuse.env")
	credentials := "LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk-test\nLANGFUSE_INIT_PROJECT_SECRET_KEY=sk-durable-test\n"
	if err := os.WriteFile(credentialsPath, []byte(credentials), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	section, _ := json.Marshal(map[string]string{"credentials_file": credentialsPath, "endpoint": endpoint, "project": "proj-1"})
	pol := &policy.PolicyFile{
		SchemaVersion: schema.PolicySchemaCurrent, DefaultSessionTTL: "8h", DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{"claude": {Resources: []string{"langfuse:project:proj-1"}, Providers: map[string]json.RawMessage{"langfuse": section}}},
	}
	audit, err := NewFileAuditLogger(auditPath)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}
	b, err := NewBroker(BrokerConfig{SocketPath: sockPath, AuditLogPath: auditPath}, NewPolicyEnforcer(pol), audit, []port.Provider{langfuseprovider.New()})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = b.Serve(ctx, ln) }()
	return b, sockPath, func() {
		cancel()
		_ = ln.Close()
		_ = audit.Close()
	}
}

func TestDurableLangfuseSecretNeverCrossesBrokerBoundary(t *testing.T) {
	var authorization string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()
	_, sockPath, cleanup := testLangfuseBroker(t, backend.URL)
	defer cleanup()

	createBody, _ := json.Marshal(api.CreateSessionRequest{AgentName: "claude", HostRepoPath: "/workspace/repo", Resources: []string{"langfuse:project:proj-1"}})
	createResp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: createBody})
	if !createResp.OK {
		t.Fatalf("create_session failed: %s", createResp.Error.Message)
	}
	var session api.CreateSessionResponse
	if err := json.Unmarshal(createResp.Body, &session); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}
	publishBody, _ := json.Marshal(api.PublishTelemetryRequest{
		SessionID: session.SessionID, BindSecret: session.BindSecret, Resource: "langfuse:project:proj-1", Payload: json.RawMessage(brokerTestOTLPPayload),
	})
	publishResp := sendRequest(t, sockPath, api.Request{Method: api.MethodPublishTelemetry, Body: publishBody})
	if !publishResp.OK {
		t.Fatalf("publish_telemetry failed: %s (%s)", publishResp.Error.Message, publishResp.Error.Code)
	}
	wire, err := json.Marshal([]api.Response{createResp, publishResp})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sk-durable-test", "pk-test", backend.URL} {
		if strings.Contains(string(wire), forbidden) {
			t.Fatalf("broker response crossed durable provider data %q", forbidden)
		}
	}
	wantAuthorization := "Basic " + base64.StdEncoding.EncodeToString([]byte("pk-test:sk-durable-test"))
	if authorization != wantAuthorization {
		t.Fatalf("upstream authorization = %q", authorization)
	}
}

func TestLangfuseIsNotRegisteredAsCredentialMinter(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer backend.Close()
	_, sockPath, cleanup := testLangfuseBroker(t, backend.URL)
	defer cleanup()
	createBody, _ := json.Marshal(api.CreateSessionRequest{AgentName: "claude", HostRepoPath: "/workspace/repo", Resources: []string{"langfuse:project:proj-1"}})
	createResp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: createBody})
	var session api.CreateSessionResponse
	_ = json.Unmarshal(createResp.Body, &session)
	mintBody, _ := json.Marshal(api.CredentialRequest{SessionID: session.SessionID, BindSecret: session.BindSecret, CredentialType: "langfuse_otlp", Resource: "langfuse:project:proj-1"})
	response := sendRequest(t, sockPath, api.Request{Method: api.MethodMintCredential, Body: mintBody})
	if response.OK || response.Error.Code != api.ErrCodeUnknownCredType {
		t.Fatalf("credential mint response = %#v", response)
	}
}

func TestBrokerDoesNotPublishTelemetryWithoutDurableAuditIntent(t *testing.T) {
	var requests int
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer backend.Close()
	b, sockPath, cleanup := testLangfuseBroker(t, backend.URL)
	defer cleanup()
	createBody, _ := json.Marshal(api.CreateSessionRequest{AgentName: "claude", HostRepoPath: "/workspace/repo", Resources: []string{"langfuse:project:proj-1"}})
	createResp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: createBody})
	var session api.CreateSessionResponse
	_ = json.Unmarshal(createResp.Body, &session)
	b.audit = &testAuditSink{err: context.Canceled}
	publishBody, _ := json.Marshal(api.PublishTelemetryRequest{SessionID: session.SessionID, BindSecret: session.BindSecret, Resource: "langfuse:project:proj-1", Payload: json.RawMessage(brokerTestOTLPPayload)})
	response := sendRequest(t, sockPath, api.Request{Method: api.MethodPublishTelemetry, Body: publishBody})
	if response.OK || response.Error.Code != api.ErrCodeBrokerUnavailable || requests != 0 {
		t.Fatalf("response = %#v, upstream requests = %d", response, requests)
	}
}

func TestBrokerRejectsTelemetryAfterSessionRevocation(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("revoked session reached upstream") }))
	defer backend.Close()
	_, sockPath, cleanup := testLangfuseBroker(t, backend.URL)
	defer cleanup()
	createBody, _ := json.Marshal(api.CreateSessionRequest{AgentName: "claude", HostRepoPath: "/workspace/repo", Resources: []string{"langfuse:project:proj-1"}})
	createResp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: createBody})
	var session api.CreateSessionResponse
	_ = json.Unmarshal(createResp.Body, &session)
	revokeBody, _ := json.Marshal(api.RevokeSessionRequest{SessionID: session.SessionID, BindSecret: session.BindSecret})
	if response := sendRequest(t, sockPath, api.Request{Method: api.MethodRevokeSession, Body: revokeBody}); !response.OK {
		t.Fatalf("revoke response = %#v", response)
	}
	publishBody, _ := json.Marshal(api.PublishTelemetryRequest{SessionID: session.SessionID, BindSecret: session.BindSecret, Resource: "langfuse:project:proj-1", Payload: json.RawMessage(brokerTestOTLPPayload)})
	response := sendRequest(t, sockPath, api.Request{Method: api.MethodPublishTelemetry, Body: publishBody})
	if response.OK || response.Error.Code != api.ErrCodeSessionExpired {
		t.Fatalf("publish response = %#v", response)
	}
}

func TestBrokerPublishesMeasurableTelemetryAuditEvidence(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	defer backend.Close()
	b, sockPath, cleanup := testLangfuseBroker(t, backend.URL)
	defer cleanup()
	createBody, _ := json.Marshal(api.CreateSessionRequest{AgentName: "claude", HostRepoPath: "/workspace/repo", Resources: []string{"langfuse:project:proj-1"}, RunID: "run_audit", TaskRef: "github:owner/repo#73"})
	createResp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: createBody})
	var session api.CreateSessionResponse
	_ = json.Unmarshal(createResp.Body, &session)
	audit := &orderedAuditSink{}
	b.audit = audit
	payload := json.RawMessage(brokerTestOTLPPayload)
	publishBody, _ := json.Marshal(api.PublishTelemetryRequest{SessionID: session.SessionID, BindSecret: session.BindSecret, Resource: "langfuse:project:proj-1", Payload: payload})
	response := sendRequest(t, sockPath, api.Request{Method: api.MethodPublishTelemetry, Body: publishBody})
	if !response.OK {
		t.Fatalf("publish response = %#v", response)
	}
	records := audit.recordedEvents()
	if len(records) != 2 || records[0].EventType != EventTelemetryPublishRequested || records[1].EventType != EventTelemetryPublished {
		t.Fatalf("audit records = %#v", records)
	}
	wantHash := sha256.Sum256(payload)
	for _, record := range records {
		if record.Metadata["payload_bytes"] != fmt.Sprintf("%d", len(payload)) || record.Metadata["payload_sha256"] != fmt.Sprintf("%x", wantHash) {
			t.Fatalf("audit metadata = %#v", record.Metadata)
		}
		if record.Metadata["run_id"] != "run_audit" || record.Metadata["task_ref"] != "github:owner/repo#73" {
			t.Fatalf("audit correlation = %#v", record.Metadata)
		}
	}
}
