package brokerclient

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
)

func TestMintCredential(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")

	credPayload, _ := json.Marshal(broker.GitHubAppInstallationCredential{Token: "ghs_test_token_xyz"})
	want := broker.CredentialResponse{
		CredentialType: broker.CredentialTypeGitHubAppInstallation,
		Resource:       "github:repo:owner/repo",
		Credential:     credPayload,
		ExpiresAt:      time.Now().Add(time.Hour),
	}

	fakeServer(t, sock, func(req broker.Request) broker.Response {
		if req.Method != broker.MethodMintCredential {
			t.Errorf("expected method %q, got %q", broker.MethodMintCredential, req.Method)
		}
		return broker.Response{
			OK:   true,
			Body: mustMarshal(t, want),
		}
	})

	client := &Client{SocketPath: sock}
	got, err := client.MintCredential(broker.CredentialRequest{
		SessionID:      "sess-1",
		BindSecret:     []byte("secret"),
		CredentialType: broker.CredentialTypeGitHubAppInstallation,
		Resource:       "github:repo:owner/repo",
	})
	if err != nil {
		t.Fatalf("MintCredential: %v", err)
	}
	if got.CredentialType != want.CredentialType {
		t.Errorf("CredentialType = %q, want %q", got.CredentialType, want.CredentialType)
	}
	if got.Resource != want.Resource {
		t.Errorf("Resource = %q, want %q", got.Resource, want.Resource)
	}

	var ghCred broker.GitHubAppInstallationCredential
	if err := json.Unmarshal(got.Credential, &ghCred); err != nil {
		t.Fatalf("unmarshal credential payload: %v", err)
	}
	if ghCred.Token != "ghs_test_token_xyz" {
		t.Errorf("Token = %q, want ghs_test_token_xyz", ghCred.Token)
	}
}

func TestMintCredentialBrokerError(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")

	fakeServer(t, sock, func(req broker.Request) broker.Response {
		return broker.Response{
			OK: false,
			Error: &broker.ErrorResponse{
				Code:    broker.ErrCodeResourceNotAllowed,
				Message: "resource not bound to this session",
			},
		}
	})

	client := &Client{SocketPath: sock}
	_, err := client.MintCredential(broker.CredentialRequest{
		SessionID:      "sess-1",
		BindSecret:     []byte("secret"),
		CredentialType: broker.CredentialTypeGitHubAppInstallation,
		Resource:       "github:repo:owner/other-repo",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	berr, ok := err.(*BrokerError)
	if !ok {
		t.Fatalf("expected *BrokerError, got %T", err)
	}
	if berr.Code != broker.ErrCodeResourceNotAllowed {
		t.Errorf("code = %q, want %q", berr.Code, broker.ErrCodeResourceNotAllowed)
	}
}
