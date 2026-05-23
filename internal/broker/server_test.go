package broker

import (
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

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

// testGitHubProvider is an in-package CredentialProvider used by broker tests.
// It is a thin stub over a fake GitHub HTTP server; subset enforcement and
// other provider-specific invariants are tested in the external
// providers/github package.
type testGitHubProvider struct {
	client *GitHubClient
	signer *Signer
}

type testGitHubConfig struct {
	InstallationID     int64
	AppID              string
	DefaultPermissions map[string]string
}

func newTestGitHubProvider(client *GitHubClient, signer *Signer) *testGitHubProvider {
	return &testGitHubProvider{client: client, signer: signer}
}

func (p *testGitHubProvider) Type() string        { return CredentialTypeGitHubAppInstallation }
func (p *testGitHubProvider) URIProvider() string { return "github" }

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

func (p *testGitHubProvider) Mint(ctx context.Context, req ProviderMintRequest) (ProviderMintResult, error) {
	cfg, ok := req.Config.(testGitHubConfig)
	if !ok {
		return ProviderMintResult{}, fmt.Errorf("test provider: unexpected config type %T", req.Config)
	}
	perms := cfg.DefaultPermissions
	if len(req.Params) > 0 && string(req.Params) != "null" {
		var pr GitHubAppInstallationParams
		if err := json.Unmarshal(req.Params, &pr); err != nil {
			return ProviderMintResult{}, err
		}
		if len(pr.Permissions) > 0 {
			perms = pr.Permissions
		}
	}
	jwt, err := p.signer.SignJWT(cfg.AppID)
	if err != nil {
		return ProviderMintResult{}, err
	}
	tok, err := p.client.MintInstallationToken(ctx, jwt, cfg.InstallationID, req.Resource.Identifier, perms)
	if err != nil {
		return ProviderMintResult{}, err
	}
	payload, err := json.Marshal(GitHubAppInstallationCredential{Token: tok.Token})
	if err != nil {
		return ProviderMintResult{}, err
	}
	return ProviderMintResult{Credential: payload, ExpiresAt: tok.ExpiresAt}, nil
}

// testBroker sets up a full broker with a mock GitHub API server and
// returns the broker, socket path, and cleanup function.
func testBroker(t *testing.T) (*Broker, string, func()) {
	t.Helper()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "broker.sock")
	auditPath := filepath.Join(dir, "audit.log")

	// Generate test PEM.
	pemPath := generateTestPEM(t, dir, "test-agent")

	// Mock GitHub API.
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

	signer, err := NewSigner(idents)
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

	ghClient := NewGitHubClient(ghServer.URL)
	provider := newTestGitHubProvider(ghClient, signer)
	b, err := NewBroker(cfg, NewPolicyEnforcer(pol, "github"), audit, []CredentialProvider{provider})
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

func sendRequest(t *testing.T, sockPath string, req Request) Response {
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

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestBrokerCreateSession(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	body, _ := json.Marshal(CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodCreateSession, Body: body})

	if !resp.OK {
		t.Fatalf("create_session failed: %s", resp.Error.Message)
	}

	var sessResp CreateSessionResponse
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

func TestBrokerCreateSessionDisallowedResource(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	body, _ := json.Marshal(CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/not-allowed"},
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodCreateSession, Body: body})

	if resp.OK {
		t.Fatal("expected error for disallowed resource")
	}
	if resp.Error.Code != ErrCodeResourceNotAllowed {
		t.Errorf("error code = %q, want %q", resp.Error.Code, ErrCodeResourceNotAllowed)
	}
}

func createTestSession(t *testing.T, sockPath string) (string, []byte) {
	t.Helper()
	body, _ := json.Marshal(CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodCreateSession, Body: body})
	if !resp.OK {
		t.Fatalf("create_session failed: %s", resp.Error.Message)
	}

	var sessResp CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &sessResp); err != nil {
		t.Fatalf("unmarshal session response: %v", err)
	}
	return sessResp.SessionID, sessResp.BindSecret
}

func TestBrokerSessionStatus(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	body, _ := json.Marshal(SessionStatusRequest{
		SessionID:  sessionID,
		BindSecret: secret,
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodSessionStatus, Body: body})

	if !resp.OK {
		t.Fatalf("session_status failed: %s", resp.Error.Message)
	}

	var statusResp SessionStatusResponse
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

	body, _ := json.Marshal(RevokeSessionRequest{
		SessionID:  sessionID,
		BindSecret: secret,
	})
	resp := sendRequest(t, sockPath, Request{Method: MethodRevokeSession, Body: body})
	if !resp.OK {
		t.Fatalf("revoke_session failed: %s", resp.Error.Message)
	}

	statusBody, _ := json.Marshal(SessionStatusRequest{
		SessionID:  sessionID,
		BindSecret: secret,
	})
	statusResp := sendRequest(t, sockPath, Request{Method: MethodSessionStatus, Body: statusBody})
	if !statusResp.OK {
		t.Fatalf("session_status after revoke failed: %s", statusResp.Error.Message)
	}

	var status SessionStatusResponse
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

	body, _ := json.Marshal(HealthCheckRequest{})
	resp := sendRequest(t, sockPath, Request{Method: MethodHealthCheck, Body: body})
	if !resp.OK {
		t.Fatalf("health_check failed: %s", resp.Error.Message)
	}

	var healthResp HealthCheckResponse
	if err := json.Unmarshal(resp.Body, &healthResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !healthResp.Healthy {
		t.Fatal("expected healthy broker response")
	}
}

func TestBrokerSessionStatusDoesNotAdvanceActivity(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	body, _ := json.Marshal(SessionStatusRequest{SessionID: sessionID, BindSecret: secret})
	resp := sendRequest(t, sockPath, Request{Method: MethodSessionStatus, Body: body})
	var s1 SessionStatusResponse
	if err := json.Unmarshal(resp.Body, &s1); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	resp2 := sendRequest(t, sockPath, Request{Method: MethodSessionStatus, Body: body})
	var s2 SessionStatusResponse
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

	resp := sendRequest(t, sockPath, Request{Method: "unknown_method", Body: json.RawMessage(`{}`)})
	if resp.OK {
		t.Fatal("expected error for unknown method")
	}
}

func TestBrokerAuditLogWritten(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)

	createTestSession(t, sockPath)
	cleanup() // Flush audit log.

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

	signer, err := NewSigner(idents)
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
	ghClient := NewGitHubClient(ghServer.URL)
	provider := newTestGitHubProvider(ghClient, signer)
	b, err := NewBroker(cfg, enforcer, audit, []CredentialProvider{provider})
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

	// Initial mint succeeds.
	mintBody, _ := json.Marshal(CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     secret,
		CredentialType: CredentialTypeGitHubAppInstallation,
		Resource:       "github:repo:owner/repo",
	})
	resp := sendRequest(t, sockPath, Request{Method: MethodMintCredential, Body: mintBody})
	if !resp.OK {
		t.Fatalf("initial mint should succeed: %s", resp.Error.Message)
	}

	// Reload policy that removes the resource.
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

	resp = sendRequest(t, sockPath, Request{Method: MethodMintCredential, Body: mintBody})
	if resp.OK {
		t.Fatal("mint should be denied after policy reload removed the resource")
	}
	if resp.Error.Code != ErrCodeResourceNotAllowed {
		t.Errorf("error code = %q, want %q", resp.Error.Code, ErrCodeResourceNotAllowed)
	}
}

func serverTestGithubSection() json.RawMessage {
	out, _ := json.Marshal(map[string]any{
		"installation_id":     42,
		"default_permissions": map[string]string{"contents": "write", "metadata": "read"},
	})
	return out
}
