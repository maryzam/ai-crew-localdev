package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
)

func createTestCredentialSession(t *testing.T, sockPath string) (string, []byte) {
	t.Helper()
	body, _ := json.Marshal(api.CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodCreateSession, Body: body})
	if !resp.OK {
		t.Fatalf("create_session failed: %s", resp.Error.Message)
	}

	var sessResp api.CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &sessResp); err != nil {
		t.Fatalf("unmarshal session response: %v", err)
	}
	return sessResp.SessionID, sessResp.BindSecret
}

func TestBrokerMintCredentialHappyPath(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestCredentialSession(t, sockPath)

	body, _ := json.Marshal(api.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     secret,
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/repo",
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodMintCredential, Body: body})
	if !resp.OK {
		t.Fatalf("mint_credential failed: %s", resp.Error.Message)
	}

	var credResp api.CredentialResponse
	if err := json.Unmarshal(resp.Body, &credResp); err != nil {
		t.Fatalf("unmarshal credential response: %v", err)
	}
	if credResp.CredentialType != githubcontract.CredentialType {
		t.Errorf("CredentialType = %q, want %q", credResp.CredentialType, githubcontract.CredentialType)
	}
	if credResp.Resource != "github:repo:owner/repo" {
		t.Errorf("Resource = %q, want github:repo:owner/repo", credResp.Resource)
	}

	var ghCred githubcontract.Credential
	if err := json.Unmarshal(credResp.Credential, &ghCred); err != nil {
		t.Fatalf("unmarshal github credential: %v", err)
	}
	if ghCred.Token != "ghs_mock_token_123" {
		t.Errorf("Token = %q, want ghs_mock_token_123", ghCred.Token)
	}
}

func TestBrokerMintCredentialUnknownCredType(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestCredentialSession(t, sockPath)

	body, _ := json.Marshal(api.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     secret,
		CredentialType: "aws_sts_session",
		Resource:       "github:repo:owner/repo",
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodMintCredential, Body: body})
	if resp.OK {
		t.Fatal("expected error for unknown credential_type")
	}
	if resp.Error.Code != api.ErrCodeUnknownCredType {
		t.Errorf("error code = %q, want %q", resp.Error.Code, api.ErrCodeUnknownCredType)
	}
}

func TestBrokerMintCredentialMalformedResource(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestCredentialSession(t, sockPath)

	body, _ := json.Marshal(api.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     secret,
		CredentialType: githubcontract.CredentialType,
		Resource:       "not-a-valid-uri",
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodMintCredential, Body: body})
	if resp.OK {
		t.Fatal("expected error for malformed resource URI")
	}
	if resp.Error.Code != api.ErrCodeInvalidResourceURI {
		t.Errorf("error code = %q, want %q", resp.Error.Code, api.ErrCodeInvalidResourceURI)
	}
}

func TestBrokerMintCredentialResourceNotInSession(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestCredentialSession(t, sockPath)

	body, _ := json.Marshal(api.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     secret,
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/other-repo",
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodMintCredential, Body: body})
	if resp.OK {
		t.Fatal("expected error for resource not bound to session")
	}
	if resp.Error.Code != api.ErrCodeResourceNotAllowed {
		t.Errorf("error code = %q, want %q", resp.Error.Code, api.ErrCodeResourceNotAllowed)
	}
}

func TestBrokerMintCredentialBindingMismatch(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, _ := createTestCredentialSession(t, sockPath)

	body, _ := json.Marshal(api.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     make([]byte, 32),
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/repo",
	})

	resp := sendRequest(t, sockPath, api.Request{Method: api.MethodMintCredential, Body: body})
	if resp.OK {
		t.Fatal("expected error for binding mismatch")
	}
	if resp.Error.Code != api.ErrCodeBindingMismatch {
		t.Errorf("error code = %q, want %q", resp.Error.Code, api.ErrCodeBindingMismatch)
	}
}

func TestBrokerMintCredentialRejectsInactiveSession(t *testing.T) {
	broker, socketPath, cleanup := testBroker(t)
	defer cleanup()
	for _, state := range []string{"expired", "idle", "revoked"} {
		t.Run(state, func(t *testing.T) {
			sessionID, secret := createTestCredentialSession(t, socketPath)
			broker.store.mu.Lock()
			session := broker.store.sessions[sessionID]
			switch state {
			case "expired":
				session.ExpiresAt = time.Now().Add(-time.Second)
			case "idle":
				session.LastActivity = time.Now().Add(-session.IdleTimeout - time.Second)
			case "revoked":
				session.Revoked = true
			}
			broker.store.mu.Unlock()
			body, _ := json.Marshal(api.CredentialRequest{SessionID: sessionID, BindSecret: secret, CredentialType: githubcontract.CredentialType, Resource: "github:repo:owner/repo"})
			response := sendRequest(t, socketPath, api.Request{Method: api.MethodMintCredential, Body: body})
			if response.OK || response.Error.Code != api.ErrCodeSessionExpired {
				t.Fatalf("response = %#v", response)
			}
		})
	}
}
