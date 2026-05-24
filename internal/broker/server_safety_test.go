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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

type recordingGithubServer struct {
	server    *httptest.Server
	mu        sync.Mutex
	callsByID map[int64]int
}

func newRecordingGithubServer(t *testing.T) *recordingGithubServer {
	t.Helper()
	r := &recordingGithubServer{callsByID: map[int64]int{}}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var installID int64
		_, _ = fmt.Sscanf(req.URL.Path, "/app/installations/%d/access_tokens", &installID)
		r.mu.Lock()
		r.callsByID[installID]++
		r.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      fmt.Sprintf("ghs_install_%d", installID),
			"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *recordingGithubServer) calls(installID int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callsByID[installID]
}

func githubSectionFor(installID int64, appID string) json.RawMessage {
	out, _ := json.Marshal(map[string]any{
		"installation_id":     installID,
		"app_id":              appID,
		"default_permissions": map[string]string{"contents": "write", "metadata": "read"},
	})
	return out
}

type safetyHarness struct {
	t        *testing.T
	dir      string
	sockPath string
	broker   *Broker
	gh       *recordingGithubServer
	cancel   context.CancelFunc
}

func newSafetyHarness(t *testing.T, agents map[string]int64) *safetyHarness {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "broker.sock")
	auditPath := filepath.Join(dir, "audit.log")
	policyPath := filepath.Join(dir, "policy.json")

	idents := &identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents:        map[string]identity.AgentIdentity{},
	}
	agentPolicies := map[string]policy.AgentPolicy{}
	for name, installID := range agents {
		pemPath := generateTestPEM(t, dir, name)
		appID := fmt.Sprintf("app-%s", name)
		idents.Agents[name] = identity.AgentIdentity{
			AppID:      appID,
			AppKey:     pemPath,
			GitName:    name + "[bot]",
			GitEmail:   name + "@bot",
			GithubHost: "github.com",
			Tool:       "test",
			Model:      "test",
		}
		agentPolicies[name] = policy.AgentPolicy{
			Resources: []string{"github:repo:owner/r"},
			Providers: map[string]json.RawMessage{"github": githubSectionFor(installID, appID)},
		}
	}

	pol := &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents:             agentPolicies,
	}
	data, _ := json.MarshalIndent(pol, "", "  ")
	if err := os.WriteFile(policyPath, data, 0600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	signer, err := NewSigner(idents)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	audit, err := NewFileAuditLogger(auditPath)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	gh := newRecordingGithubServer(t)
	provider := newTestGitHubProvider(NewGitHubClient(gh.server.URL), signer)
	b, err := NewBroker(BrokerConfig{
		SocketPath:   sockPath,
		PolicyPath:   policyPath,
		AuditLogPath: auditPath,
	}, NewPolicyEnforcer(pol, "github"), audit, []CredentialProvider{provider})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = b.Serve(ctx, ln) }()

	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		_ = audit.Close()
	})
	return &safetyHarness{t: t, dir: dir, sockPath: sockPath, broker: b, gh: gh, cancel: cancel}
}

func (h *safetyHarness) mintFor(agent, resource string) Response {
	h.t.Helper()
	body, _ := json.Marshal(CreateSessionRequest{
		AgentName:    agent,
		HostRepoPath: "/workspace/repo",
		Resources:    []string{resource},
	})
	resp := sendRequest(h.t, h.sockPath, Request{Method: MethodCreateSession, Body: body})
	if !resp.OK {
		h.t.Fatalf("create_session for %s: %s", agent, resp.Error.Message)
	}
	var sessResp CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &sessResp); err != nil {
		h.t.Fatalf("unmarshal session: %v", err)
	}
	mintBody, _ := json.Marshal(CredentialRequest{
		SessionID:      sessResp.SessionID,
		BindSecret:     sessResp.BindSecret,
		CredentialType: CredentialTypeGitHubAppInstallation,
		Resource:       resource,
	})
	return sendRequest(h.t, h.sockPath, Request{Method: MethodMintCredential, Body: mintBody})
}

func extractMintedToken(t *testing.T, resp Response) string {
	t.Helper()
	if !resp.OK {
		t.Fatalf("mint failed: %s", resp.Error.Message)
	}
	var cr CredentialResponse
	if err := json.Unmarshal(resp.Body, &cr); err != nil {
		t.Fatalf("unmarshal CredentialResponse: %v", err)
	}
	var gc GitHubAppInstallationCredential
	if err := json.Unmarshal(cr.Credential, &gc); err != nil {
		t.Fatalf("unmarshal github credential: %v", err)
	}
	return gc.Token
}

// Cross-agent cache isolation: two agents sharing a resource but configured
// with different GitHub installations must not share cached credentials.
func TestBrokerCacheIsolatedAcrossAgents(t *testing.T) {
	h := newSafetyHarness(t, map[string]int64{
		"claude": 42,
		"codex":  7,
	})

	respClaude := h.mintFor("claude", "github:repo:owner/r")
	respCodex := h.mintFor("codex", "github:repo:owner/r")

	tokenClaude := extractMintedToken(t, respClaude)
	tokenCodex := extractMintedToken(t, respCodex)

	if tokenClaude == tokenCodex {
		t.Fatalf("agents with different installations received the same cached token %q", tokenClaude)
	}
	if got := h.gh.calls(42); got != 1 {
		t.Errorf("installation 42 upstream calls = %d, want 1", got)
	}
	if got := h.gh.calls(7); got != 1 {
		t.Errorf("installation 7 upstream calls = %d, want 1", got)
	}
}

// Mint with credential_type that does not serve the resource's URI provider
// must be rejected before any provider config lookup or upstream call.
func TestBrokerRejectsCredentialTypeResourceMismatch(t *testing.T) {
	h := newSafetyHarness(t, map[string]int64{"claude": 42})

	body, _ := json.Marshal(CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/r"},
	})
	resp := sendRequest(t, h.sockPath, Request{Method: MethodCreateSession, Body: body})
	if !resp.OK {
		t.Fatalf("create_session: %s", resp.Error.Message)
	}
	var sessResp CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &sessResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mintBody, _ := json.Marshal(CredentialRequest{
		SessionID:      sessResp.SessionID,
		BindSecret:     sessResp.BindSecret,
		CredentialType: "aws_assume_role",
		Resource:       "github:repo:owner/r",
	})
	mintResp := sendRequest(t, h.sockPath, Request{Method: MethodMintCredential, Body: mintBody})
	if mintResp.OK {
		t.Fatal("expected mismatch to be rejected")
	}
	if mintResp.Error.Code != ErrCodeUnknownCredType {
		t.Errorf("error code = %q, want %q", mintResp.Error.Code, ErrCodeUnknownCredType)
	}
	if h.gh.calls(42) != 0 {
		t.Errorf("upstream must not be called on mismatch, got %d calls", h.gh.calls(42))
	}
}

// Reload failure must leave the broker on the previous policy and configs:
// existing mints continue to work as if reload never happened.
func TestBrokerReloadFailureLeavesPriorStateIntact(t *testing.T) {
	h := newSafetyHarness(t, map[string]int64{"claude": 42})

	before := h.mintFor("claude", "github:repo:owner/r")
	if !before.OK {
		t.Fatalf("pre-reload mint should succeed: %s", before.Error.Message)
	}

	policyPath := filepath.Join(h.dir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"schema_version":"wrong/v99"}`), 0600); err != nil {
		t.Fatalf("write bad policy: %v", err)
	}

	if err := h.broker.ReloadPolicy(); err == nil {
		t.Fatal("expected ReloadPolicy to fail on invalid file")
	}

	after := h.mintFor("claude", "github:repo:owner/r")
	if !after.OK {
		t.Fatalf("post-failed-reload mint should still succeed: %s", after.Error.Message)
	}
}

// A mint that started before a reload that removed the resource must not
// observe a torn snapshot where AuthorizeResource passes against the old
// policy but the new agent config is used to mint. The authorize check and
// the config load must observe the same policy/configs snapshot.
func TestBrokerMintAfterReloadRemovingResourceIsRejected(t *testing.T) {
	h := newSafetyHarness(t, map[string]int64{"claude": 42})

	body, _ := json.Marshal(CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/r"},
	})
	resp := sendRequest(t, h.sockPath, Request{Method: MethodCreateSession, Body: body})
	if !resp.OK {
		t.Fatalf("create_session: %s", resp.Error.Message)
	}
	var sessResp CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &sessResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	updated := &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				Resources: []string{"github:repo:owner/different"},
				Providers: map[string]json.RawMessage{"github": githubSectionFor(42, "app-claude")},
			},
		},
	}
	data, _ := json.MarshalIndent(updated, "", "  ")
	if err := os.WriteFile(filepath.Join(h.dir, "policy.json"), data, 0600); err != nil {
		t.Fatalf("rewrite policy: %v", err)
	}
	if err := h.broker.ReloadPolicy(); err != nil {
		t.Fatalf("ReloadPolicy: %v", err)
	}

	mintBody, _ := json.Marshal(CredentialRequest{
		SessionID:      sessResp.SessionID,
		BindSecret:     sessResp.BindSecret,
		CredentialType: CredentialTypeGitHubAppInstallation,
		Resource:       "github:repo:owner/r",
	})
	mintResp := sendRequest(t, h.sockPath, Request{Method: MethodMintCredential, Body: mintBody})
	if mintResp.OK {
		t.Fatal("mint must be rejected after reload removed the resource from policy")
	}
	if mintResp.Error.Code != ErrCodeResourceNotAllowed {
		t.Errorf("error code = %q, want %q", mintResp.Error.Code, ErrCodeResourceNotAllowed)
	}
	if h.gh.calls(42) != 0 {
		t.Errorf("no upstream call expected, got %d", h.gh.calls(42))
	}
}

// Successful reload that changes provider config must invalidate cached
// credentials so the next mint goes upstream against the new identity.
func TestBrokerReloadClearsCacheOnConfigChange(t *testing.T) {
	h := newSafetyHarness(t, map[string]int64{"claude": 42})

	_ = h.mintFor("claude", "github:repo:owner/r")
	if got := h.gh.calls(42); got != 1 {
		t.Fatalf("setup: installation 42 calls = %d, want 1", got)
	}

	_ = h.mintFor("claude", "github:repo:owner/r")
	if got := h.gh.calls(42); got != 1 {
		t.Fatalf("second mint should hit cache, but installation 42 calls = %d", got)
	}

	updated := &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				Resources: []string{"github:repo:owner/r"},
				Providers: map[string]json.RawMessage{"github": githubSectionFor(99, "app-claude")},
			},
		},
	}
	data, _ := json.MarshalIndent(updated, "", "  ")
	if err := os.WriteFile(filepath.Join(h.dir, "policy.json"), data, 0600); err != nil {
		t.Fatalf("rewrite policy: %v", err)
	}
	if err := h.broker.ReloadPolicy(); err != nil {
		t.Fatalf("ReloadPolicy: %v", err)
	}

	_ = h.mintFor("claude", "github:repo:owner/r")
	if got := h.gh.calls(42); got != 1 {
		t.Errorf("after reload, original installation should not be hit again, calls = %d", got)
	}
	if got := h.gh.calls(99); got != 1 {
		t.Errorf("after reload, new installation should be hit fresh, calls = %d", got)
	}
}

// Concurrent reload and mint must not race or panic. Run with -race.
func TestBrokerConcurrentReloadAndMint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent test in short mode")
	}
	h := newSafetyHarness(t, map[string]int64{"claude": 42})

	body, _ := json.Marshal(CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/r"},
	})
	resp := sendRequest(t, h.sockPath, Request{Method: MethodCreateSession, Body: body})
	if !resp.OK {
		t.Fatalf("create_session: %s", resp.Error.Message)
	}
	var sessResp CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &sessResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mintBody, _ := json.Marshal(CredentialRequest{
		SessionID:      sessResp.SessionID,
		BindSecret:     sessResp.BindSecret,
		CredentialType: CredentialTypeGitHubAppInstallation,
		Resource:       "github:repo:owner/r",
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	var mintErrors atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				r := sendRequest(t, h.sockPath, Request{Method: MethodMintCredential, Body: mintBody})
				switch {
				case r.OK:
				case r.Error.Code == ErrCodeBrokerUnavailable:
				case r.Error.Code == ErrCodeRateLimited:
				default:
					mintErrors.Add(1)
				}
			}
		}()
	}

	for i := 0; i < 5; i++ {
		if err := h.broker.ReloadPolicy(); err != nil {
			t.Errorf("ReloadPolicy: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	wg.Wait()

	if mintErrors.Load() > 0 {
		t.Errorf("got %d unexpected mint errors during concurrent reload", mintErrors.Load())
	}
}
