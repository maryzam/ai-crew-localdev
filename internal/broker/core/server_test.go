package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	githubprovider "github.com/maryzam/ai-crew-localdev/internal/providers/github"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
)

type testGitHubProvider struct {
	client *githubprovider.GitHubClient
	signer *githubprovider.Signer
}

type testGitHubConfig struct {
	InstallationID     int64
	AppID              string
	DefaultPermissions map[string]string
}

func newTestGitHubProvider(client *githubprovider.GitHubClient, signer *githubprovider.Signer) *testGitHubProvider {
	return &testGitHubProvider{client: client, signer: signer}
}

func (p *testGitHubProvider) Type() string        { return githubcontract.CredentialType }
func (p *testGitHubProvider) URIProvider() string { return "github" }

func (p *testGitHubProvider) ValidateResource(uri api.ResourceURI) error {
	if uri.Provider != "github" || uri.Kind != "repo" {
		return fmt.Errorf("test provider: unsupported %s:%s", uri.Provider, uri.Kind)
	}
	return nil
}

func (p *testGitHubProvider) ParseConfig(agent string, section json.RawMessage) (any, error) {
	var raw struct {
		InstallationID     int64             `json:"installation_id"`
		AppID              string            `json:"app_id"`
		DefaultPermissions map[string]string `json:"default_permissions"`
	}
	if err := json.Unmarshal(section, &raw); err != nil {
		return nil, fmt.Errorf("test provider ParseConfig: %w", err)
	}
	if raw.AppID == "" {
		raw.AppID = "12345"
	}
	return testGitHubConfig{
		InstallationID:     raw.InstallationID,
		AppID:              raw.AppID,
		DefaultPermissions: raw.DefaultPermissions,
	}, nil
}

func (p *testGitHubProvider) PrepareMint(params json.RawMessage, _ any) (string, error) {
	if len(params) == 0 || string(params) == "null" {
		return "", nil
	}
	return string(params), nil
}

func (p *testGitHubProvider) Mint(ctx context.Context, req port.ProviderMintRequest) (port.ProviderMintResult, error) {
	cfg, ok := req.Config.(testGitHubConfig)
	if !ok {
		return port.ProviderMintResult{}, fmt.Errorf("test provider: unexpected config type %T", req.Config)
	}
	perms := cfg.DefaultPermissions
	if len(req.Params) > 0 && string(req.Params) != "null" {
		var pr githubcontract.Params
		if err := json.Unmarshal(req.Params, &pr); err != nil {
			return port.ProviderMintResult{}, err
		}
		if len(pr.Permissions) > 0 {
			perms = pr.Permissions
		}
	}
	jwt, err := p.signer.SignJWT(cfg.AppID)
	if err != nil {
		return port.ProviderMintResult{}, err
	}
	tok, err := p.client.MintInstallationToken(ctx, jwt, cfg.InstallationID, req.Resource.Identifier, perms)
	if err != nil {
		return port.ProviderMintResult{}, err
	}
	payload, err := json.Marshal(githubcontract.Credential{Token: tok.Token})
	if err != nil {
		return port.ProviderMintResult{}, err
	}
	return port.ProviderMintResult{Credential: payload, ExpiresAt: tok.ExpiresAt}, nil
}

func testBroker(t *testing.T) (*Broker, string, func()) {
	t.Helper()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "broker.sock")
	auditPath := filepath.Join(dir, "audit.log")

	pemPath := generateTestPEM(t, dir, "test-agent")

	ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"token":      "ghs_mock_token_123",
			"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	}))

	idents := &identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents: map[string]identity.AgentIdentity{
			"claude": {
				AppID:      "12345",
				AppKey:     pemPath,
				GitName:    "claude[bot]",
				GitEmail:   "claude@bot",
				GithubHost: "github.com",
				Tool:       "claude-code",
				Model:      "claude-sonnet-4-6",
			},
		},
	}

	pol := &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				Resources: []string{"github:repo:owner/repo"},
				Providers: map[string]json.RawMessage{"github": serverTestGithubSection()},
			},
		},
	}

	signer, err := githubprovider.NewSigner(idents)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	audit, err := NewFileAuditLogger(auditPath)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	cfg := BrokerConfig{
		SocketPath:   sockPath,
		AuditLogPath: auditPath,
	}

	ghClient := githubprovider.NewGitHubClient(ghServer.URL)
	provider := newTestGitHubProvider(ghClient, signer)
	b, err := NewBroker(cfg, NewPolicyEnforcer(pol, "github"), audit, []port.Provider{provider})
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
		ghServer.Close()
	}

	return b, sockPath, cleanup
}

func sendRequest(t *testing.T, sockPath string, req api.Request) api.Response {
	t.Helper()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	var resp api.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestBrokerCreateSession(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	body, _ := json.Marshal(api.CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: body})

	if !resp.OK {
		t.Fatalf("create_session failed: %s", resp.Error.Message)
	}

	var sessResp api.CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &sessResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if sessResp.SessionID == "" {
		t.Error("session ID should not be empty")
	}
	if len(sessResp.BindSecret) != bindSecretLen {
		t.Errorf("bind secret length = %d, want %d", len(sessResp.BindSecret), bindSecretLen)
	}
}

func TestBrokerAuthorizeResourcesUsesSessionPolicyWithoutCreatingSession(t *testing.T) {
	broker, sockPath, cleanup := testBroker(t)
	defer cleanup()

	body, _ := json.Marshal(api.AuthorizeResourcesRequest{
		AgentName: "claude",
		Resources: []string{"github:repo:owner/repo"},
		RunID:     "run_authorize_test",
		TaskRef:   "github:owner/repo#43",
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodAuthorizeResources, Body: body})

	if !resp.OK {
		t.Fatalf("authorize_resources failed: %s", resp.Error.Message)
	}
	var auth api.AuthorizeResourcesResponse
	if err := json.Unmarshal(resp.Body, &auth); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	broker.store.mu.RLock()
	sessionCount := len(broker.store.sessions)
	broker.store.mu.RUnlock()
	if sessionCount != 0 {
		t.Fatalf("authorize_resources created %d session(s), want none", sessionCount)
	}
}

func TestBrokerCreateSessionDisallowedResource(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	body, _ := json.Marshal(api.CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/not-allowed"},
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: body})

	if resp.OK {
		t.Fatal("expected error for disallowed resource")
	}
	if resp.Error.Code != api.ErrCodeResourceNotAllowed {
		t.Errorf("error code = %q, want %q", resp.Error.Code, api.ErrCodeResourceNotAllowed)
	}
}

func TestBrokerAuthorizeResourcesDeniesDisallowedResource(t *testing.T) {
	broker, sockPath, cleanup := testBroker(t)
	defer cleanup()
	audit := &orderedAuditSink{}
	broker.audit = audit

	body, _ := json.Marshal(api.AuthorizeResourcesRequest{
		AgentName: "claude",
		Resources: []string{"github:repo:owner/not-allowed"},
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodAuthorizeResources, Body: body})

	if resp.OK {
		t.Fatal("expected error for disallowed resource")
	}
	if resp.Error.Code != api.ErrCodeResourceNotAllowed {
		t.Errorf("error code = %q, want %q", resp.Error.Code, api.ErrCodeResourceNotAllowed)
	}
	events := audit.recorded()
	if len(events) != 1 || events[0] != EventResourcesDenied {
		t.Fatalf("audit events = %v, want [%s]", events, EventResourcesDenied)
	}
}

func createTestSession(t *testing.T, sockPath string) (string, []byte) {
	t.Helper()
	body, _ := json.Marshal(api.CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: body})
	if !resp.OK {
		t.Fatalf("create_session failed: %s", resp.Error.Message)
	}

	var sessResp api.CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &sessResp); err != nil {
		t.Fatalf("unmarshal session response: %v", err)
	}
	return sessResp.SessionID, sessResp.BindSecret
}

func TestBrokerSessionStatus(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	body, _ := json.Marshal(api.SessionStatusRequest{
		SessionID:  sessionID,
		BindSecret: secret,
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodSessionStatus, Body: body})

	if !resp.OK {
		t.Fatalf("session_status failed: %s", resp.Error.Message)
	}

	var statusResp api.SessionStatusResponse
	if err := json.Unmarshal(resp.Body, &statusResp); err != nil {
		t.Fatalf("unmarshal status response: %v", err)
	}

	if !statusResp.Active {
		t.Error("session should be active")
	}
	if statusResp.AgentName != "claude" {
		t.Errorf("AgentName = %q, want claude", statusResp.AgentName)
	}
	if len(statusResp.Resources) != 1 || statusResp.Resources[0] != "github:repo:owner/repo" {
		t.Errorf("Resources = %v, want [github:repo:owner/repo]", statusResp.Resources)
	}
}

func TestBrokerRevokeSession(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	body, _ := json.Marshal(api.RevokeSessionRequest{
		SessionID:  sessionID,
		BindSecret: secret,
	})
	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodRevokeSession, Body: body})
	if !resp.OK {
		t.Fatalf("revoke_session failed: %s", resp.Error.Message)
	}

	statusBody, _ := json.Marshal(api.SessionStatusRequest{
		SessionID:  sessionID,
		BindSecret: secret,
	})
	statusResp := sendRequest(t, sockPath, api.Request{Method: api.MethodSessionStatus, Body: statusBody})
	if !statusResp.OK {
		t.Fatalf("session_status after revoke failed: %s", statusResp.Error.Message)
	}

	var status api.SessionStatusResponse
	if err := json.Unmarshal(statusResp.Body, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.Active {
		t.Error("revoked session should not be active")
	}
}

func TestBrokerSessionStatusAndRevokeDoNotRequireBindSecret(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, _ := createTestSession(t, sockPath)

	statusBody, _ := json.Marshal(api.SessionStatusRequest{SessionID: sessionID})
	statusResp := sendRequest(t, sockPath, api.Request{Method: api.MethodSessionStatus, Body: statusBody})
	if !statusResp.OK {
		t.Fatalf("session_status without bind secret failed: %s", statusResp.Error.Message)
	}

	revokeBody, _ := json.Marshal(api.RevokeSessionRequest{SessionID: sessionID})
	revokeResp := sendRequest(t, sockPath, api.Request{Method: api.MethodRevokeSession, Body: revokeBody})
	if !revokeResp.OK {
		t.Fatalf("revoke_session without bind secret failed: %s", revokeResp.Error.Message)
	}

	statusResp = sendRequest(t, sockPath, api.Request{Method: api.MethodSessionStatus, Body: statusBody})
	if !statusResp.OK {
		t.Fatalf("session_status after revoke failed: %s", statusResp.Error.Message)
	}
	var status api.SessionStatusResponse
	if err := json.Unmarshal(statusResp.Body, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.Active {
		t.Error("revoked session should not be active")
	}
}

func TestBrokerHealthCheck(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	body, _ := json.Marshal(api.HealthCheckRequest{})
	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodHealthCheck, Body: body})
	if !resp.OK {
		t.Fatalf("health_check failed: %s", resp.Error.Message)
	}

	var healthResp api.HealthCheckResponse
	if err := json.Unmarshal(resp.Body, &healthResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !healthResp.Healthy {
		t.Fatal("expected healthy broker response")
	}
}

func TestBrokerRejectsInvalidCorrelationMetadata(t *testing.T) {
	_, socketPath, cleanup := testBroker(t)
	defer cleanup()

	body, err := json.Marshal(api.CreateSessionRequest{
		AgentName: "claude", Resources: []string{"github:repo:owner/repo"}, RunID: "run_with space",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := sendRequest(t, socketPath, api.Request{Method: api.MethodCreateSession, Body: body})
	if response.OK || response.Error == nil || response.Error.Code != api.ErrCodeInvalidCorrelation {
		t.Fatalf("response = %#v", response)
	}
}

func TestBrokerSessionStatusDoesNotAdvanceActivity(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	body, _ := json.Marshal(api.SessionStatusRequest{SessionID: sessionID, BindSecret: secret})
	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodSessionStatus, Body: body})
	var s1 api.SessionStatusResponse
	if err := json.Unmarshal(resp.Body, &s1); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	resp2 := sendRequest(t, sockPath, api.Request{Method: api.MethodSessionStatus, Body: body})
	var s2 api.SessionStatusResponse
	if err := json.Unmarshal(resp2.Body, &s2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !s1.LastActivity.Equal(s2.LastActivity) {
		t.Error("session_status should not advance LastActivity")
	}
}

func TestBrokerUnknownMethod(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	resp := sendRequest(t, sockPath, api.Request{Method: "unknown_method", Body: json.RawMessage(`{}`)})
	if resp.OK {
		t.Fatal("expected error for unknown method")
	}
}

func TestBrokerAuditLogWritten(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)

	createTestSession(t, sockPath)
	cleanup()

	auditDir := filepath.Dir(sockPath)
	auditPath := filepath.Join(auditDir, "audit.log")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}

	if len(data) == 0 {
		t.Error("audit log should not be empty after session creation")
	}
}

func TestBrokerAuditLogIncludesRunIDMetadata(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)

	body, _ := json.Marshal(api.CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
		RunID:        "run_audit_test",
		TaskRef:      "github:owner/repo#43",
	})
	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: body})
	if !resp.OK {
		t.Fatalf("create_session failed: %s", resp.Error.Message)
	}
	cleanup()

	auditPath := filepath.Join(filepath.Dir(sockPath), "audit.log")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}

	var event AuditEvent
	if err := json.Unmarshal(bytes.TrimSpace(data), &event); err != nil {
		t.Fatalf("unmarshal audit event: %v", err)
	}
	if event.Metadata["run_id"] != "run_audit_test" {
		t.Fatalf("audit run_id metadata = %q, want run_audit_test", event.Metadata["run_id"])
	}
	if event.Metadata["task_ref"] != "github:owner/repo#43" {
		t.Fatalf("audit task_ref metadata = %q, want github:owner/repo#43", event.Metadata["task_ref"])
	}
}

func TestBrokerDeniedCreateSessionAuditIncludesRunIDMetadata(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)

	body, _ := json.Marshal(api.CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"not-a-uri"},
		RunID:        "run_denied_audit_test",
		TaskRef:      "github:owner/repo#43",
	})
	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: body})
	if resp.OK {
		t.Fatal("expected create_session denial")
	}
	cleanup()

	auditPath := filepath.Join(filepath.Dir(sockPath), "audit.log")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}

	var event AuditEvent
	if err := json.Unmarshal(bytes.TrimSpace(data), &event); err != nil {
		t.Fatalf("unmarshal audit event: %v", err)
	}
	if event.Success {
		t.Fatal("audit event should record denied create_session")
	}
	if event.Metadata["run_id"] != "run_denied_audit_test" {
		t.Fatalf("audit run_id metadata = %q, want run_denied_audit_test", event.Metadata["run_id"])
	}
	if event.Metadata["task_ref"] != "github:owner/repo#43" {
		t.Fatalf("audit task_ref metadata = %q, want github:owner/repo#43", event.Metadata["task_ref"])
	}
}

func TestBrokerMintCredentialDeniedAfterPolicyReload(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "broker.sock")
	auditPath := filepath.Join(dir, "audit.log")
	policyPath := filepath.Join(dir, "policy.json")

	pemPath := generateTestPEM(t, dir, "test-agent")

	initialPolicy := policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				Resources: []string{"github:repo:owner/repo"},
				Providers: map[string]json.RawMessage{"github": serverTestGithubSection()},
			},
		},
	}
	data, _ := json.MarshalIndent(initialPolicy, "", "  ")
	if err := os.WriteFile(policyPath, data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"token":      "ghs_mock_token_123",
			"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	}))
	defer ghServer.Close()

	idents := &identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents: map[string]identity.AgentIdentity{
			"claude": {
				AppID:      "12345",
				AppKey:     pemPath,
				GitName:    "claude[bot]",
				GitEmail:   "claude@bot",
				GithubHost: "github.com",
				Tool:       "claude-code",
				Model:      "claude-sonnet-4-6",
			},
		},
	}

	signer, err := githubprovider.NewSigner(idents)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	audit, err := NewFileAuditLogger(auditPath)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}
	defer func() { _ = audit.Close() }()

	cfg := BrokerConfig{
		SocketPath:   sockPath,
		PolicyPath:   policyPath,
		AuditLogPath: auditPath,
	}

	enforcer := NewPolicyEnforcer(&initialPolicy, "github")
	ghClient := githubprovider.NewGitHubClient(ghServer.URL)
	provider := newTestGitHubProvider(ghClient, signer)
	b, err := NewBroker(cfg, enforcer, audit, []port.Provider{provider})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = ln.Close()
	}()
	go func() { _ = b.Serve(ctx, ln) }()

	sessionID, secret := createTestSession(t, sockPath)

	mintBody, _ := json.Marshal(api.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     secret,
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/repo",
	})
	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodMintCredential, Body: mintBody})
	if !resp.OK {
		t.Fatalf("initial mint should succeed: %s", resp.Error.Message)
	}

	restricted := policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				Resources: []string{"github:repo:owner/other-repo"},
				Providers: map[string]json.RawMessage{"github": serverTestGithubSection()},
			},
		},
	}
	data, _ = json.MarshalIndent(restricted, "", "  ")
	if err := os.WriteFile(policyPath, data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := b.ReloadPolicy(); err != nil {
		t.Fatalf("ReloadPolicy: %v", err)
	}

	resp = sendRequest(t, sockPath, api.Request{Method: api.MethodMintCredential, Body: mintBody})
	if resp.OK {
		t.Fatal("mint should be denied after policy reload removed the resource")
	}
	if resp.Error.Code != api.ErrCodeResourceNotAllowed {
		t.Errorf("error code = %q, want %q", resp.Error.Code, api.ErrCodeResourceNotAllowed)
	}
}

func serverTestGithubSection() json.RawMessage {
	out, _ := json.Marshal(map[string]any{
		"installation_id":     42,
		"default_permissions": map[string]string{"contents": "write", "metadata": "read"},
	})
	return out
}
