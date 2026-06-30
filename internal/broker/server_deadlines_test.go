package broker

import (
	"context"
	"encoding/json"
	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
	"github.com/maryzam/ai-crew-localdev/internal/brokerport"
	githubprovider "github.com/maryzam/ai-crew-localdev/internal/providers/github"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

func TestBrokerSlowUpstreamMintStillResponds(t *testing.T) {
	upstreamDelay := connWriteTimeout + 2*time.Second
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "broker.sock")
	auditPath := filepath.Join(dir, "audit.log")
	policyPath := filepath.Join(dir, "policy.json")
	pemPath := generateTestPEM(t, dir, "claude")

	ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(upstreamDelay)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_slow",
			"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	}))
	defer ghServer.Close()

	idents := &identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents: map[string]identity.AgentIdentity{
			"claude": {
				AppID: "12345", AppKey: pemPath,
				GitName: "claude[bot]", GitEmail: "claude@bot",
				GithubHost: "github.com", Tool: "claude-code", Model: "test",
			},
		},
	}
	section, _ := json.Marshal(map[string]any{
		"installation_id":     42,
		"app_id":              "12345",
		"default_permissions": map[string]string{"contents": "write", "metadata": "read"},
	})
	pol := &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				Resources: []string{"github:repo:owner/repo"},
				Providers: map[string]json.RawMessage{"github": section},
			},
		},
	}
	data, _ := json.MarshalIndent(pol, "", "  ")
	if err := os.WriteFile(policyPath, data, 0600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	signer, err := githubprovider.NewSigner(idents)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	audit, err := NewFileAuditLogger(auditPath)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}
	t.Cleanup(func() { _ = audit.Close() })

	provider := newTestGitHubProvider(githubprovider.NewGitHubClient(ghServer.URL), signer)
	b, err := NewBroker(BrokerConfig{
		SocketPath:   sockPath,
		PolicyPath:   policyPath,
		AuditLogPath: auditPath,
	}, NewPolicyEnforcer(pol, "github"), audit, []brokerport.CredentialProvider{provider})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = ln.Close() })
	go func() { _ = b.Serve(ctx, ln) }()

	sessBody, _ := json.Marshal(brokerapi.CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
	})
	resp := sendRequestWithTimeout(t, sockPath, brokerapi.Request{Method: brokerapi.MethodCreateSession, Body: sessBody}, 30*time.Second)
	if !resp.OK {
		t.Fatalf("create_session: %s", resp.Error.Message)
	}
	var sessResp brokerapi.CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &sessResp); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}

	mintBody, _ := json.Marshal(brokerapi.CredentialRequest{
		SessionID:      sessResp.SessionID,
		BindSecret:     sessResp.BindSecret,
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/repo",
	})
	mintResp := sendRequestWithTimeout(t, sockPath, brokerapi.Request{Method: brokerapi.MethodMintCredential, Body: mintBody}, 30*time.Second)
	if !mintResp.OK {
		t.Fatalf("mint should succeed despite slow upstream: %s", mintResp.Error.Message)
	}
	var cr brokerapi.CredentialResponse
	if err := json.Unmarshal(mintResp.Body, &cr); err != nil {
		t.Fatalf("unmarshal brokerapi.CredentialResponse: %v", err)
	}
	var gc githubcontract.Credential
	if err := json.Unmarshal(cr.Credential, &gc); err != nil {
		t.Fatalf("unmarshal github credential: %v", err)
	}
	if gc.Token != "ghs_slow" {
		t.Errorf("Token = %q, want ghs_slow", gc.Token)
	}
}

func sendRequestWithTimeout(t *testing.T, sockPath string, req brokerapi.Request, timeout time.Duration) brokerapi.Response {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var resp brokerapi.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestConnWriteTimeoutSmallerThanUpstream(t *testing.T) {
	upstream := 30 * time.Second
	if connWriteTimeout >= upstream {
		t.Errorf("connWriteTimeout (%v) >= upstream (%v): test premise no longer holds",
			connWriteTimeout, upstream)
	}
	if connWriteTimeout <= 0 {
		t.Errorf("connWriteTimeout must be positive, got %v", connWriteTimeout)
	}
}
