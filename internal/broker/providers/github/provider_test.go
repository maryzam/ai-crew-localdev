package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
)

const testAppID = "12345"

func newTestSigner(t *testing.T) *broker.Signer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	dir := t.TempDir()
	pemPath := filepath.Join(dir, "test.pem")
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := os.WriteFile(pemPath, pemData, 0600); err != nil {
		t.Fatalf("write PEM: %v", err)
	}

	idents := &identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents: map[string]identity.AgentIdentity{
			"test-agent": {
				AppID:      testAppID,
				AppKey:     pemPath,
				GitName:    "test[bot]",
				GitEmail:   "test@bot",
				GithubHost: "github.com",
				Tool:       "test",
				Model:      "test",
			},
		},
	}

	signer, err := broker.NewSigner(idents)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return signer
}

func newTestProvider(t *testing.T, serverURL string) *Provider {
	t.Helper()
	return New(broker.NewGitHubClient(serverURL), newTestSigner(t), func(string) string { return testAppID })
}

func defaultConfig() Config {
	return Config{
		InstallationID: 42,
		AppID:          testAppID,
		DefaultPermissions: map[string]string{
			"contents":      "write",
			"pull_requests": "write",
			"metadata":      "read",
		},
	}
}

func TestProviderType(t *testing.T) {
	p := New(nil, nil, nil)
	if got, want := p.Type(), broker.CredentialTypeGitHubAppInstallation; got != want {
		t.Errorf("Type() = %q, want %q", got, want)
	}
	if got, want := p.URIProvider(), "github"; got != want {
		t.Errorf("URIProvider() = %q, want %q", got, want)
	}
}

func TestProviderMintDownscope(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	var captured map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_ok",
			"expires_at": expires.Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	p := newTestProvider(t, srv.URL)
	params, _ := json.Marshal(broker.GitHubAppInstallationParams{
		Permissions: map[string]string{"contents": "read"},
	})

	res, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/r"},
		Params:   params,
		Agent:    "test-agent",
		Config:   defaultConfig(),
	})
	if err != nil {
		t.Fatalf("Mint downscope: %v", err)
	}

	var cred broker.GitHubAppInstallationCredential
	if err := json.Unmarshal(res.Credential, &cred); err != nil {
		t.Fatalf("unmarshal credential: %v", err)
	}
	if cred.Token != "ghs_ok" {
		t.Errorf("Token = %q", cred.Token)
	}

	perms, _ := captured["permissions"].(map[string]any)
	if perms["contents"] != "read" {
		t.Errorf("upstream permissions.contents = %v, want read", perms["contents"])
	}
}

func TestProviderMintRejectsEscalation(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv.URL)
	params, _ := json.Marshal(broker.GitHubAppInstallationParams{
		Permissions: map[string]string{"metadata": "write"},
	})

	_, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/r"},
		Params:   params,
		Agent:    "test-agent",
		Config:   defaultConfig(),
	})
	if err == nil {
		t.Fatal("expected escalation to be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds policy default") {
		t.Errorf("error = %v, want subset-violation message", err)
	}
	if called {
		t.Error("upstream GitHub was called despite escalation attempt")
	}
}

func TestProviderMintRejectsUnknownPermissionKey(t *testing.T) {
	p := newTestProvider(t, "")
	params, _ := json.Marshal(broker.GitHubAppInstallationParams{
		Permissions: map[string]string{"workflows": "write"},
	})
	_, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "o/r"},
		Params:   params,
		Agent:    "test-agent",
		Config:   defaultConfig(),
	})
	if err == nil || !strings.Contains(err.Error(), "not granted by policy") {
		t.Errorf("err = %v, want not-granted-by-policy", err)
	}
}

func TestProviderMintUsesDefaultsWhenParamsEmpty(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_default",
			"expires_at": "2099-01-01T00:00:00Z",
		})
	}))
	defer srv.Close()

	p := newTestProvider(t, srv.URL)
	_, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "o/r"},
		Agent:    "test-agent",
		Config:   defaultConfig(),
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	perms, _ := captured["permissions"].(map[string]any)
	if perms["metadata"] != "read" || perms["contents"] != "write" {
		t.Errorf("expected defaults, got %v", perms)
	}
}

func TestProviderMintWrongResourceKind(t *testing.T) {
	p := newTestProvider(t, "")
	_, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "github", Kind: "org", Identifier: "acme"},
		Agent:    "test-agent",
		Config:   defaultConfig(),
	})
	if err == nil {
		t.Fatal("expected error for non-repo resource")
	}
}

func TestProviderMintBadConfigType(t *testing.T) {
	p := newTestProvider(t, "")
	_, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "o/r"},
		Agent:    "test-agent",
		Config:   "not a config",
	})
	if err == nil {
		t.Fatal("expected error for wrong config type")
	}
}

func TestProviderMintBadParams(t *testing.T) {
	p := newTestProvider(t, "")
	_, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "o/r"},
		Params:   json.RawMessage(`{not-json`),
		Agent:    "test-agent",
		Config:   defaultConfig(),
	})
	if err == nil {
		t.Fatal("expected error for malformed params")
	}
}

func TestPrepareMintRejectsEscalation(t *testing.T) {
	p := newTestProvider(t, "")
	params, _ := json.Marshal(broker.GitHubAppInstallationParams{
		Permissions: map[string]string{"metadata": "write"},
	})
	_, err := p.PrepareMint(params, defaultConfig())
	if err == nil {
		t.Fatal("PrepareMint must reject escalation before Mint is called")
	}
}

func TestPrepareMintCacheKeyChangesWithInstallationID(t *testing.T) {
	p := newTestProvider(t, "")
	base := defaultConfig()
	other := base
	other.InstallationID = base.InstallationID + 1

	keyBase, err := p.PrepareMint(nil, base)
	if err != nil {
		t.Fatalf("PrepareMint base: %v", err)
	}
	keyOther, err := p.PrepareMint(nil, other)
	if err != nil {
		t.Fatalf("PrepareMint other: %v", err)
	}
	if keyBase == keyOther {
		t.Fatal("cache key must differ when installation_id changes")
	}
}

func TestPrepareMintCacheKeyChangesWithAppID(t *testing.T) {
	p := newTestProvider(t, "")
	base := defaultConfig()
	other := base
	other.AppID = base.AppID + "-rotated"

	keyBase, err := p.PrepareMint(nil, base)
	if err != nil {
		t.Fatalf("PrepareMint base: %v", err)
	}
	keyOther, err := p.PrepareMint(nil, other)
	if err != nil {
		t.Fatalf("PrepareMint other: %v", err)
	}
	if keyBase == keyOther {
		t.Fatal("cache key must differ when app_id changes")
	}
}

func TestPrepareMintStableCacheKey(t *testing.T) {
	p := newTestProvider(t, "")
	a, errA := p.PrepareMint(nil, defaultConfig())
	if errA != nil {
		t.Fatalf("PrepareMint a: %v", errA)
	}
	b, errB := p.PrepareMint(json.RawMessage(`null`), defaultConfig())
	if errB != nil {
		t.Fatalf("PrepareMint b: %v", errB)
	}
	if a != b {
		t.Errorf("empty and null params should yield same cache key, got %q vs %q", a, b)
	}

	params, _ := json.Marshal(broker.GitHubAppInstallationParams{
		Permissions: map[string]string{"contents": "read"},
	})
	c, errC := p.PrepareMint(params, defaultConfig())
	if errC != nil {
		t.Fatalf("PrepareMint c: %v", errC)
	}
	if c == a {
		t.Errorf("different params should yield different cache keys, both %q", a)
	}
}
