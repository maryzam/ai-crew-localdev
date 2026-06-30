package brokerclient

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
)

func TestMintCredential(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")

	credPayload, _ := json.Marshal(githubcontract.Credential{Token: "ghs_test_token_xyz"})
	want := brokerapi.CredentialResponse{
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/repo",
		Credential:     credPayload,
		ExpiresAt:      time.Now().Add(time.Hour),
	}

	fakeServer(t, sock, func(req brokerapi.Request) brokerapi.Response {
		if req.Method != brokerapi.MethodMintCredential {
			t.Errorf("expected method %q, got %q", brokerapi.MethodMintCredential, req.Method)
		}
		return brokerapi.Response{
			OK:   true,
			Body: mustMarshal(t, want),
		}
	})

	client := &Client{SocketPath: sock}
	got, err := client.MintCredential(brokerapi.CredentialRequest{
		SessionID:      "sess-1",
		BindSecret:     []byte("secret"),
		CredentialType: githubcontract.CredentialType,
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

	var ghCred githubcontract.Credential
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

	fakeServer(t, sock, func(req brokerapi.Request) brokerapi.Response {
		return brokerapi.Response{
			OK: false,
			Error: &brokerapi.ErrorResponse{
				Code:    brokerapi.ErrCodeResourceNotAllowed,
				Message: "resource not bound to this session",
			},
		}
	})

	client := &Client{SocketPath: sock}
	_, err := client.MintCredential(brokerapi.CredentialRequest{
		SessionID:      "sess-1",
		BindSecret:     []byte("secret"),
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/other-repo",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	berr, ok := err.(*BrokerError)
	if !ok {
		t.Fatalf("expected *BrokerError, got %T", err)
	}
	if berr.Code != brokerapi.ErrCodeResourceNotAllowed {
		t.Errorf("code = %q, want %q", berr.Code, brokerapi.ErrCodeResourceNotAllowed)
	}
}
