package langfuse

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
)

func TestProviderMintsCredentialFromOwnerOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	data := []byte("LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk-test\nLANGFUSE_INIT_PROJECT_SECRET_KEY='sk-test'\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	provider := New()
	config, err := provider.ParseConfig("codex", configJSON(t, path))
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "langfuse", Kind: "project", Identifier: "managed-runs"},
		Config:   config,
	})
	if err != nil {
		t.Fatal(err)
	}
	var credential broker.LangfuseOTLPCredential
	if err := json.Unmarshal(result.Credential, &credential); err != nil {
		t.Fatal(err)
	}
	if credential.Endpoint != "http://localhost:3000" || credential.PublicKey != "pk-test" || credential.SecretKey != "sk-test" {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestProviderRejectsInsecureCredentialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk\nLANGFUSE_INIT_PROJECT_SECRET_KEY=sk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := New()
	config, err := provider.ParseConfig("codex", configJSON(t, path))
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "langfuse", Kind: "project", Identifier: "managed-runs"},
		Config:   config,
	})
	if err == nil {
		t.Fatal("insecure credentials file accepted")
	}
}

func TestProviderRejectsProjectMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk\nLANGFUSE_INIT_PROJECT_SECRET_KEY=sk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := New()
	config, err := provider.ParseConfig("codex", configJSON(t, path))
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Mint(context.Background(), broker.ProviderMintRequest{
		Resource: broker.ResourceURI{Provider: "langfuse", Kind: "project", Identifier: "other"},
		Config:   config,
	})
	if err == nil {
		t.Fatal("project mismatch accepted")
	}
}

func TestProviderRejectsCredentialFileSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "credentials.env")
	if err := os.WriteFile(target, []byte("LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk\nLANGFUSE_INIT_PROJECT_SECRET_KEY=sk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "credentials.env")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	provider := New()
	config, err := provider.ParseConfig("codex", configJSON(t, link))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.PrepareMint(nil, config); err == nil {
		t.Fatal("credential file symlink accepted")
	}
}

func configJSON(t *testing.T, path string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(rawConfig{
		CredentialsFile: path,
		Endpoint:        "http://localhost:3000",
		Project:         "managed-runs",
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
