package broker

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

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
		json.NewEncoder(w).Encode(map[string]interface{}{
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

	instID := int64(42)
	pol := &policy.PolicyFile{
		SchemaVersion:      "ai-agent-policy/v1",
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				AllowedRepos:       []string{"owner/repo"},
				InstallationID:     &instID,
				DefaultPermissions: map[string]string{"contents": "write", "metadata": "read"},
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
		GitHubBaseURL: ghServer.URL,
	}

	b := NewBroker(cfg, idents, NewPolicyEnforcer(pol), signer, audit)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go b.Serve(ctx, ln)

	cleanup := func() {
		cancel()
		ln.Close()
		audit.Close()
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
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

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
		AgentName:            "claude",
		Repo:                 "owner/repo",
		HostRepoPath:         "/workspace/repo",
		RequestedPermissions: map[string]string{"contents": "write"},
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

func TestBrokerCreateSessionDisallowedRepo(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	body, _ := json.Marshal(CreateSessionRequest{
		AgentName:    "claude",
		Repo:         "owner/not-allowed",
		HostRepoPath: "/workspace/repo",
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodCreateSession, Body: body})

	if resp.OK {
		t.Fatal("expected error for disallowed repo")
	}
	if resp.Error.Code != ErrCodeRepoNotAllowed {
		t.Errorf("error code = %q, want %q", resp.Error.Code, ErrCodeRepoNotAllowed)
	}
}

func createTestSession(t *testing.T, sockPath string) (string, []byte) {
	t.Helper()
	body, _ := json.Marshal(CreateSessionRequest{
		AgentName:            "claude",
		Repo:                 "owner/repo",
		HostRepoPath:         "/workspace/repo",
		RequestedPermissions: map[string]string{"contents": "write", "metadata": "read"},
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodCreateSession, Body: body})
	if !resp.OK {
		t.Fatalf("create_session failed: %s", resp.Error.Message)
	}

	var sessResp CreateSessionResponse
	json.Unmarshal(resp.Body, &sessResp)
	return sessResp.SessionID, sessResp.BindSecret
}

func TestBrokerMintToken(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	body, _ := json.Marshal(TokenRequest{
		SessionID:  sessionID,
		BindSecret: secret,
		Repo:       "owner/repo",
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodMintToken, Body: body})

	if !resp.OK {
		t.Fatalf("mint_token failed: %s", resp.Error.Message)
	}

	var tokenResp TokenResponse
	json.Unmarshal(resp.Body, &tokenResp)

	if tokenResp.Token != "ghs_mock_token_123" {
		t.Errorf("Token = %q, want ghs_mock_token_123", tokenResp.Token)
	}
	if tokenResp.Repo != "owner/repo" {
		t.Errorf("Repo = %q, want owner/repo", tokenResp.Repo)
	}
}

func TestBrokerMintTokenWrongBinding(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, _ := createTestSession(t, sockPath)

	body, _ := json.Marshal(TokenRequest{
		SessionID:  sessionID,
		BindSecret: make([]byte, 32), // wrong secret
		Repo:       "owner/repo",
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodMintToken, Body: body})

	if resp.OK {
		t.Fatal("expected error for wrong binding")
	}
	if resp.Error.Code != ErrCodeBindingMismatch {
		t.Errorf("error code = %q, want %q", resp.Error.Code, ErrCodeBindingMismatch)
	}
}

func TestBrokerMintTokenWrongRepo(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	body, _ := json.Marshal(TokenRequest{
		SessionID:  sessionID,
		BindSecret: secret,
		Repo:       "owner/other-repo", // not the session-bound repo
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodMintToken, Body: body})

	if resp.OK {
		t.Fatal("expected error for wrong repo")
	}
	if resp.Error.Code != ErrCodeRepoNotAllowed {
		t.Errorf("error code = %q, want %q", resp.Error.Code, ErrCodeRepoNotAllowed)
	}
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
	json.Unmarshal(resp.Body, &statusResp)

	if !statusResp.Active {
		t.Error("session should be active")
	}
	if statusResp.AgentName != "claude" {
		t.Errorf("AgentName = %q, want claude", statusResp.AgentName)
	}
	if statusResp.Repo != "owner/repo" {
		t.Errorf("Repo = %q, want owner/repo", statusResp.Repo)
	}
}

func TestBrokerRevokeSession(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	// Revoke.
	body, _ := json.Marshal(RevokeSessionRequest{
		SessionID:  sessionID,
		BindSecret: secret,
	})
	resp := sendRequest(t, sockPath, Request{Method: MethodRevokeSession, Body: body})
	if !resp.OK {
		t.Fatalf("revoke_session failed: %s", resp.Error.Message)
	}

	// Verify session is no longer active.
	statusBody, _ := json.Marshal(SessionStatusRequest{
		SessionID:  sessionID,
		BindSecret: secret,
	})
	statusResp := sendRequest(t, sockPath, Request{Method: MethodSessionStatus, Body: statusBody})
	if !statusResp.OK {
		t.Fatalf("session_status after revoke failed: %s", statusResp.Error.Message)
	}

	var status SessionStatusResponse
	json.Unmarshal(statusResp.Body, &status)
	if status.Active {
		t.Error("revoked session should not be active")
	}
}

func TestBrokerMintTokenRevokedSession(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	// Revoke the session.
	revokeBody, _ := json.Marshal(RevokeSessionRequest{SessionID: sessionID, BindSecret: secret})
	sendRequest(t, sockPath, Request{Method: MethodRevokeSession, Body: revokeBody})

	// Try to mint a token.
	mintBody, _ := json.Marshal(TokenRequest{SessionID: sessionID, BindSecret: secret, Repo: "owner/repo"})
	resp := sendRequest(t, sockPath, Request{Method: MethodMintToken, Body: mintBody})

	if resp.OK {
		t.Fatal("expected error for revoked session")
	}
	if resp.Error.Code != ErrCodeSessionExpired {
		t.Errorf("error code = %q, want %q", resp.Error.Code, ErrCodeSessionExpired)
	}
}

func TestBrokerSessionStatusDoesNotAdvanceActivity(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	// Get initial activity time.
	body, _ := json.Marshal(SessionStatusRequest{SessionID: sessionID, BindSecret: secret})
	resp := sendRequest(t, sockPath, Request{Method: MethodSessionStatus, Body: body})
	var s1 SessionStatusResponse
	json.Unmarshal(resp.Body, &s1)

	time.Sleep(10 * time.Millisecond)

	// Query status again.
	resp2 := sendRequest(t, sockPath, Request{Method: MethodSessionStatus, Body: body})
	var s2 SessionStatusResponse
	json.Unmarshal(resp2.Body, &s2)

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

	// Read audit log.
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

func TestBrokerMintTokenWithDownscope(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	// Request with narrower permissions.
	body, _ := json.Marshal(TokenRequest{
		SessionID:   sessionID,
		BindSecret:  secret,
		Repo:        "owner/repo",
		Permissions: map[string]string{"metadata": "read"},
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodMintToken, Body: body})
	if !resp.OK {
		t.Fatalf("mint_token with downscope failed: %s", resp.Error.Message)
	}
}

func TestBrokerMintTokenPermissionEscalation(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestSession(t, sockPath)

	// Request with broader permissions than session.
	body, _ := json.Marshal(TokenRequest{
		SessionID:   sessionID,
		BindSecret:  secret,
		Repo:        "owner/repo",
		Permissions: map[string]string{"contents": "admin"},
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodMintToken, Body: body})
	if resp.OK {
		t.Fatal("expected error for permission escalation")
	}
	if resp.Error.Code != ErrCodePermissionDenied {
		t.Errorf("error code = %q, want %q", resp.Error.Code, ErrCodePermissionDenied)
	}
}

func TestBrokerSessionNotFound(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	body, _ := json.Marshal(TokenRequest{
		SessionID:  "nonexistent-session",
		BindSecret: make([]byte, 32),
		Repo:       "owner/repo",
	})

	resp := sendRequest(t, sockPath, Request{Method: MethodMintToken, Body: body})
	if resp.OK {
		t.Fatal("expected error for nonexistent session")
	}
	if resp.Error.Code != ErrCodeSessionNotFound {
		t.Errorf("error code = %q, want %q", resp.Error.Code, ErrCodeSessionNotFound)
	}
}
