package core

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
	langfuseprovider "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse"
	langfusecontract "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse/contract"
)

func testLangfuseBroker(t *testing.T) (string, func()) {
	t.Helper()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "broker.sock")
	auditPath := filepath.Join(dir, "audit.log")

	credentialsPath := filepath.Join(dir, "langfuse.env")
	credentials := "LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk-test\nLANGFUSE_INIT_PROJECT_SECRET_KEY=sk-test\n"
	if err := os.WriteFile(credentialsPath, []byte(credentials), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	section, _ := json.Marshal(map[string]string{
		"credentials_file": credentialsPath,
		"endpoint":         "http://localhost:3000/api/public/otel",
		"project":          "proj-1",
	})

	pol := &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				Resources: []string{"langfuse:project:proj-1"},
				Providers: map[string]json.RawMessage{"langfuse": section},
			},
		},
	}

	audit, err := NewFileAuditLogger(auditPath)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	b, err := NewBroker(BrokerConfig{SocketPath: sockPath, AuditLogPath: auditPath},
		NewPolicyEnforcer(pol), audit, []port.CredentialProvider{langfuseprovider.New()})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = b.Serve(ctx, ln) }()

	cleanup := func() {
		cancel()
		_ = ln.Close()
		_ = audit.Close()
	}
	return sockPath, cleanup
}

func TestBrokerMintLangfuseCredential(t *testing.T) {
	sockPath, cleanup := testLangfuseBroker(t)
	defer cleanup()

	createBody, _ := json.Marshal(api.CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"langfuse:project:proj-1"},
	})
	createResp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: createBody})
	if !createResp.OK {
		t.Fatalf("create_session failed: %s", createResp.Error.Message)
	}
	var session api.CreateSessionResponse
	if err := json.Unmarshal(createResp.Body, &session); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}

	mintBody, _ := json.Marshal(api.CredentialRequest{
		SessionID:      session.SessionID,
		BindSecret:     session.BindSecret,
		CredentialType: langfusecontract.CredentialType,
		Resource:       "langfuse:project:proj-1",
	})
	mintResp := sendRequest(t, sockPath, api.Request{Method: api.MethodMintCredential, Body: mintBody})
	if !mintResp.OK {
		t.Fatalf("mint_credential failed: %s (%s)", mintResp.Error.Message, mintResp.Error.Code)
	}

	var credential api.CredentialResponse
	if err := json.Unmarshal(mintResp.Body, &credential); err != nil {
		t.Fatalf("unmarshal credential response: %v", err)
	}
	var payload langfusecontract.Credential
	if err := json.Unmarshal(credential.Credential, &payload); err != nil {
		t.Fatalf("unmarshal langfuse credential: %v", err)
	}
	if payload.PublicKey != "pk-test" || payload.SecretKey != "sk-test" {
		t.Fatalf("unexpected langfuse credential keys: %+v", payload)
	}
	if payload.Endpoint != "http://localhost:3000/api/public/otel" {
		t.Fatalf("unexpected langfuse endpoint: %q", payload.Endpoint)
	}
}
