package broker

import (
	"encoding/json"
	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
	"testing"
)

// createTestCredentialSession creates a session using the credential-generic
// Resources field on brokerapi.CreateSessionRequest. It returns the new session ID and
// the bind secret so callers can issue mint_credential requests.
func createTestCredentialSession(t *testing.T, sockPath string) (string, []byte) {
	t.Helper()
	body, _ := json.Marshal(brokerapi.CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
	})

	resp := sendRequest(t, sockPath, brokerapi.Request{Method: brokerapi.MethodCreateSession, Body: body})
	if !resp.OK {
		t.Fatalf("create_session failed: %s", resp.Error.Message)
	}

	var sessResp brokerapi.CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &sessResp); err != nil {
		t.Fatalf("unmarshal session response: %v", err)
	}
	return sessResp.SessionID, sessResp.BindSecret
}

func TestBrokerMintCredentialHappyPath(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestCredentialSession(t, sockPath)

	body, _ := json.Marshal(brokerapi.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     secret,
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/repo",
	})

	resp := sendRequest(t, sockPath, brokerapi.Request{Method: brokerapi.MethodMintCredential, Body: body})
	if !resp.OK {
		t.Fatalf("mint_credential failed: %s", resp.Error.Message)
	}

	var credResp brokerapi.CredentialResponse
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

	body, _ := json.Marshal(brokerapi.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     secret,
		CredentialType: "aws_sts_session",
		Resource:       "github:repo:owner/repo",
	})

	resp := sendRequest(t, sockPath, brokerapi.Request{Method: brokerapi.MethodMintCredential, Body: body})
	if resp.OK {
		t.Fatal("expected error for unknown credential_type")
	}
	if resp.Error.Code != brokerapi.ErrCodeUnknownCredType {
		t.Errorf("error code = %q, want %q", resp.Error.Code, brokerapi.ErrCodeUnknownCredType)
	}
}

func TestBrokerMintCredentialMalformedResource(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestCredentialSession(t, sockPath)

	body, _ := json.Marshal(brokerapi.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     secret,
		CredentialType: githubcontract.CredentialType,
		Resource:       "not-a-valid-uri",
	})

	resp := sendRequest(t, sockPath, brokerapi.Request{Method: brokerapi.MethodMintCredential, Body: body})
	if resp.OK {
		t.Fatal("expected error for malformed resource URI")
	}
	if resp.Error.Code != brokerapi.ErrCodeInvalidResourceURI {
		t.Errorf("error code = %q, want %q", resp.Error.Code, brokerapi.ErrCodeInvalidResourceURI)
	}
}

func TestBrokerMintCredentialResourceNotInSession(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestCredentialSession(t, sockPath)

	body, _ := json.Marshal(brokerapi.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     secret,
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/other-repo",
	})

	resp := sendRequest(t, sockPath, brokerapi.Request{Method: brokerapi.MethodMintCredential, Body: body})
	if resp.OK {
		t.Fatal("expected error for resource not bound to session")
	}
	if resp.Error.Code != brokerapi.ErrCodeResourceNotAllowed {
		t.Errorf("error code = %q, want %q", resp.Error.Code, brokerapi.ErrCodeResourceNotAllowed)
	}
}

func TestBrokerMintCredentialBindingMismatch(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, _ := createTestCredentialSession(t, sockPath)

	body, _ := json.Marshal(brokerapi.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     make([]byte, 32),
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/repo",
	})

	resp := sendRequest(t, sockPath, brokerapi.Request{Method: brokerapi.MethodMintCredential, Body: body})
	if resp.OK {
		t.Fatal("expected error for binding mismatch")
	}
	if resp.Error.Code != brokerapi.ErrCodeBindingMismatch {
		t.Errorf("error code = %q, want %q", resp.Error.Code, brokerapi.ErrCodeBindingMismatch)
	}
}

func TestBrokerMintCredentialSessionExpired(t *testing.T) {
	b, sockPath, cleanup := testBroker(t)
	defer cleanup()

	sessionID, secret := createTestCredentialSession(t, sockPath)

	// Revoke the session to render it inactive.
	if err := b.store.Revoke(sessionID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	body, _ := json.Marshal(brokerapi.CredentialRequest{
		SessionID:      sessionID,
		BindSecret:     secret,
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:owner/repo",
	})

	resp := sendRequest(t, sockPath, brokerapi.Request{Method: brokerapi.MethodMintCredential, Body: body})
	if resp.OK {
		t.Fatal("expected error for inactive session")
	}
	if resp.Error.Code != brokerapi.ErrCodeSessionExpired {
		t.Errorf("error code = %q, want %q", resp.Error.Code, brokerapi.ErrCodeSessionExpired)
	}
}
