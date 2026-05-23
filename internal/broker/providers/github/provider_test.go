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

func TestProviderType(t *testing.T) {
	p := New(nil, nil)
	if got, want := p.Type(), broker.CredentialTypeGitHubAppInstallation; got != want {
		t.Errorf("Type() = %q, want %q", got, want)
	}
}

func TestProviderMint(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	var capturedAuth string
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/42/access_tokens" {
			t.Errorf("path = %s, want /app/installations/42/access_tokens", r.URL.Path)
		}
		capturedAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_provider_test",
			"expires_at": expires.Format(time.RFC3339),
		})
	}))
	defer server.Close()

	client := broker.NewGitHubClient(server.URL)
	signer := newTestSigner(t)
	p := New(client, signer)

	params, _ := json.Marshal(broker.GitHubAppInstallationParams{
		Permissions: map[string]string{"contents": "read"},
	})

	res, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/my-repo"},
		Params:   params,
		Agent:    "test-agent",
		ProviderConfig: Config{
			InstallationID:     42,
			AppID:              testAppID,
			DefaultPermissions: map[string]string{"metadata": "read"},
		},
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	if !res.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", res.ExpiresAt, expires)
	}

	var cred broker.GitHubAppInstallationCredential
	if err := json.Unmarshal(res.Credential, &cred); err != nil {
		t.Fatalf("unmarshal credential: %v", err)
	}
	if cred.Token != "ghs_provider_test" {
		t.Errorf("Token = %q, want ghs_provider_test", cred.Token)
	}

	if capturedAuth == "" || capturedAuth[:7] != "Bearer " {
		t.Errorf("Authorization = %q, want a Bearer JWT", capturedAuth)
	}

	// Params.Permissions should win over DefaultPermissions.
	perms, ok := capturedBody["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions missing or wrong type: %v", capturedBody["permissions"])
	}
	if perms["contents"] != "read" {
		t.Errorf("permissions.contents = %v, want read", perms["contents"])
	}
	if _, present := perms["metadata"]; present {
		t.Errorf("permissions should not include default metadata when params override; got %v", perms)
	}

	repos, _ := capturedBody["repositories"].([]any)
	if len(repos) != 1 || repos[0] != "my-repo" {
		t.Errorf("repositories = %v, want [my-repo]", repos)
	}
}

func TestProviderMintDefaultPermissions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		perms, _ := body["permissions"].(map[string]any)
		if perms["metadata"] != "read" {
			t.Errorf("expected default metadata=read, got %v", perms)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_default",
			"expires_at": "2099-01-01T00:00:00Z",
		})
	}))
	defer server.Close()

	p := New(broker.NewGitHubClient(server.URL), newTestSigner(t))

	_, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/r"},
		Agent:    "test-agent",
		ProviderConfig: Config{
			InstallationID:     42,
			AppID:              testAppID,
			DefaultPermissions: map[string]string{"metadata": "read"},
		},
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
}

func TestProviderMintWrongResourceKind(t *testing.T) {
	p := New(broker.NewGitHubClient(""), newTestSigner(t))
	_, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource:       broker.ResourceURI{Provider: "github", Kind: "org", Identifier: "acme"},
		Agent:          "test-agent",
		ProviderConfig: Config{InstallationID: 1, AppID: testAppID},
	})
	if err == nil {
		t.Fatal("expected error for non-repo resource")
	}
}

func TestProviderMintBadConfigType(t *testing.T) {
	p := New(broker.NewGitHubClient(""), newTestSigner(t))
	_, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource:       broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "o/r"},
		Agent:          "test-agent",
		ProviderConfig: "not a config",
	})
	if err == nil {
		t.Fatal("expected error for wrong ProviderConfig type")
	}
}

func TestProviderMintBadParams(t *testing.T) {
	p := New(broker.NewGitHubClient(""), newTestSigner(t))
	_, err := p.Mint(context.Background(), broker.ProviderMintRequest{
		Resource:       broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "o/r"},
		Params:         json.RawMessage(`{not-json`),
		Agent:          "test-agent",
		ProviderConfig: Config{InstallationID: 1, AppID: testAppID},
	})
	if err == nil {
		t.Fatal("expected error for malformed params")
	}
}
