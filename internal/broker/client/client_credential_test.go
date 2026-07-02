package client

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
)

func TestMintCredential(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")

	credPayload, _ := json.Marshal(githubcontract.Credential{Token: "ghs_test_token_xyz"})
	want := api.CredentialResponse{
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/repo",
		Credential:     credPayload,
		ExpiresAt:      time.Now().Add(time.Hour),
	}

	fakeServer(t, sock, func(req api.Request) api.Response {
		if req.Method != api.MethodMintCredential {
			t.Errorf("expected method %q, got %q", api.MethodMintCredential, req.Method)
		}
		return api.Response{
			OK:   true,
			Body: mustMarshal(t, want),
		}
	})

	client := &Client{SocketPath: sock}
	got, err := client.MintCredential(api.CredentialRequest{
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

	fakeServer(t, sock, func(req api.Request) api.Response {
		return api.Response{
			OK: false,
			Error: &api.ErrorResponse{
				Code:    api.ErrCodeResourceNotAllowed,
				Message: "resource not bound to this session",
			},
		}
	})

	client := &Client{SocketPath: sock}
	_, err := client.MintCredential(api.CredentialRequest{
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
	if berr.Code != api.ErrCodeResourceNotAllowed {
		t.Errorf("code = %q, want %q", berr.Code, api.ErrCodeResourceNotAllowed)
	}
}

func TestPublishTelemetry(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")
	payload := json.RawMessage(`{"resourceSpans":[]}`)
	fakeServer(t, sock, func(req api.Request) api.Response {
		if req.Method != api.MethodPublishTelemetry {
			t.Errorf("method = %q", req.Method)
		}
		var publish api.PublishTelemetryRequest
		if err := json.Unmarshal(req.Body, &publish); err != nil {
			t.Fatal(err)
		}
		if publish.Resource != "langfuse:project:managed-runs" || string(publish.Payload) != string(payload) {
			t.Fatalf("publish request = %#v", publish)
		}
		return api.Response{OK: true, Body: mustMarshal(t, api.PublishTelemetryResponse{AcceptedBytes: len(payload)})}
	})
	got, err := (&Client{SocketPath: sock}).PublishTelemetry(api.PublishTelemetryRequest{
		SessionID: "sess-1", BindSecret: []byte("secret"), Resource: "langfuse:project:managed-runs", Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.AcceptedBytes != len(payload) {
		t.Fatalf("accepted bytes = %d", got.AcceptedBytes)
	}
}
