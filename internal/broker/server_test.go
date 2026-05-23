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

// testGitHubProvider is an in-package CredentialProvider used by broker
// tests. It calls the broker's GitHubClient and Signer directly so the
// tests do not need to import the external github provider package
// (which itself imports broker, which would create an import cycle).
type testGitHubProvider struct {
	b *Broker
}

func newTestGitHubProvider(b *Broker) *testGitHubProvider { return &testGitHubProvider{b: b} }

func (p *testGitHubProvider) Type() string { return CredentialTypeGitHubAppInstallation }

func (p *testGitHubProvider) Mint(ctx context.Context, req ProviderMintRequest) (ProviderMintResult, error) {
	cfg, ok := req.ProviderConfig.(GitHubProviderConfig)
	if !ok {
		return ProviderMintResult{}, fmt.Errorf("test provider: unexpected config type %T", req.ProviderConfig)
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
	jwt, err := p.b.Signer().SignJWT(cfg.AppID)
	if err != nil {
		return ProviderMintResult{}, err
	}
	tok, err := p.b.GitHubClient().MintInstallationToken(ctx, jwt, cfg.InstallationID, req.Resource.Identifier, perms)
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
				GitHub: &policy.GitHubAgentConfig{
					InstallationID:     42,
					DefaultPermissions: map[string]string{"contents": "write", "metadata": "read"},
				},
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
		SocketPath:    sockPath,
		AuditLogPath:  auditPath,
		GitHubBaseURL: ghServer.URL,
	}

	b := NewBroker(cfg, idents, NewPolicyEnforcer(pol), signer, audit)
	b.RegisterProvider(newTestGitHubProvider(b))

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
				GitHub: &policy.GitHubAgentConfig{
					InstallationID:     42,
					DefaultPermissions: map[string]string{"contents": "write", "metadata": "read"},
				},
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
		SocketPath:    sockPath,
		PolicyPath:    policyPath,
		AuditLogPath:  auditPath,
		GitHubBaseURL: ghServer.URL,
	}

	enforcer := NewPolicyEnforcer(&initialPolicy)
	b := NewBroker(cfg, idents, enforcer, signer, audit)
	b.RegisterProvider(newTestGitHubProvider(b))

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
				GitHub: &policy.GitHubAgentConfig{
					InstallationID:     42,
					DefaultPermissions: map[string]string{"contents": "write", "metadata": "read"},
				},
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
