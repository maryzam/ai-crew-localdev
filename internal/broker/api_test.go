package broker

import (
	"encoding/json"
	"testing"
	"time"
)

func TestErrorResponseRoundTrip(t *testing.T) {
	orig := ErrorResponse{
		Code:    ErrCodeSessionNotFound,
		Message: "session not found",
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ErrorResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Code != orig.Code {
		t.Errorf("Code = %q, want %q", got.Code, orig.Code)
	}
	if got.Message != orig.Message {
		t.Errorf("Message = %q, want %q", got.Message, orig.Message)
	}
}

func TestRequestEnvelopeRoundTrip(t *testing.T) {
	body, _ := json.Marshal(CredentialRequest{
		SessionID:      "sess-1",
		BindSecret:     []byte("s"),
		CredentialType: CredentialTypeGitHubAppInstallation,
		Resource:       "github:repo:o/r",
	})
	orig := Request{
		Method: MethodMintCredential,
		Body:   body,
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Method != orig.Method {
		t.Errorf("Method = %q, want %q", got.Method, orig.Method)
	}

	var inner CredentialRequest
	if err := json.Unmarshal(got.Body, &inner); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if inner.SessionID != "sess-1" {
		t.Errorf("inner SessionID = %q, want %q", inner.SessionID, "sess-1")
	}
}

func TestResponseEnvelopeSuccess(t *testing.T) {
	body, _ := json.Marshal(CredentialResponse{
		CredentialType: CredentialTypeGitHubAppInstallation,
		Resource:       "github:repo:o/r",
		Credential:     json.RawMessage(`{"token":"tok"}`),
		ExpiresAt:      time.Now(),
	})
	orig := Response{OK: true, Body: body}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Response
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.OK {
		t.Error("expected OK = true")
	}
	if got.Error != nil {
		t.Error("expected Error to be nil")
	}
}

func TestResponseEnvelopeError(t *testing.T) {
	orig := Response{
		OK:    false,
		Error: &ErrorResponse{Code: ErrCodeBindingMismatch, Message: "bad secret"},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Response
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OK {
		t.Error("expected OK = false")
	}
	if got.Error == nil {
		t.Fatal("expected Error to be non-nil")
	}
	if got.Error.Code != ErrCodeBindingMismatch {
		t.Errorf("Error.Code = %q, want %q", got.Error.Code, ErrCodeBindingMismatch)
	}
}

func TestCreateSessionRequestRoundTrip(t *testing.T) {
	orig := CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/home/user/repo",
		Resources:    []string{"github:repo:owner/repo"},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got CreateSessionRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.AgentName != orig.AgentName {
		t.Errorf("AgentName = %q, want %q", got.AgentName, orig.AgentName)
	}
	if got.HostRepoPath != orig.HostRepoPath {
		t.Errorf("HostRepoPath = %q, want %q", got.HostRepoPath, orig.HostRepoPath)
	}
	if len(got.Resources) != 1 || got.Resources[0] != "github:repo:owner/repo" {
		t.Errorf("Resources = %v, want [github:repo:owner/repo]", got.Resources)
	}
}

func TestCreateSessionResponseRoundTrip(t *testing.T) {
	orig := CreateSessionResponse{
		SessionID:   "sess-abc",
		BindSecret:  []byte("raw-secret-32-bytes-of-csprng!!"),
		ExpiresAt:   time.Date(2026, 3, 11, 20, 0, 0, 0, time.UTC),
		IdleTimeout: DurationString(time.Hour),
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got CreateSessionResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SessionID != orig.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, orig.SessionID)
	}
	if string(got.BindSecret) != string(orig.BindSecret) {
		t.Errorf("BindSecret mismatch")
	}
	if !got.ExpiresAt.Equal(orig.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, orig.ExpiresAt)
	}
	if got.IdleTimeout != orig.IdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", got.IdleTimeout, orig.IdleTimeout)
	}
}

func TestCreateSessionResponseIdleTimeoutWireFormat(t *testing.T) {
	// idle_timeout must serialize as a JSON string, not a nanosecond integer,
	// so that non-Go clients can read the socket protocol without special-casing
	// Go's time.Duration encoding.
	resp := CreateSessionResponse{
		SessionID:   "sess-wire",
		IdleTimeout: DurationString(90 * time.Minute),
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	idleRaw, ok := raw["idle_timeout"]
	if !ok {
		t.Fatal("idle_timeout field missing from JSON output")
	}
	var s string
	if err := json.Unmarshal(idleRaw, &s); err != nil {
		t.Errorf("idle_timeout is not a JSON string: %s (parse error: %v)", idleRaw, err)
	}
	if s != "1h30m0s" {
		t.Errorf("idle_timeout wire value = %q, want %q", s, "1h30m0s")
	}
}

func TestRevokeSessionRoundTrip(t *testing.T) {
	orig := RevokeSessionRequest{
		SessionID:  "sess-xyz",
		BindSecret: []byte("secret"),
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got RevokeSessionRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SessionID != orig.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, orig.SessionID)
	}

	respOrig := RevokeSessionResponse{Revoked: true}
	data, err = json.Marshal(respOrig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var respGot RevokeSessionResponse
	if err := json.Unmarshal(data, &respGot); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !respGot.Revoked {
		t.Error("expected Revoked = true")
	}
}

func TestSessionStatusRoundTrip(t *testing.T) {
	orig := SessionStatusRequest{
		SessionID:  "sess-status",
		BindSecret: []byte("secret"),
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got SessionStatusRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SessionID != orig.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, orig.SessionID)
	}

	now := time.Now().UTC().Truncate(time.Second)
	respOrig := SessionStatusResponse{
		Active:       true,
		AgentName:    "claude",
		Resources:    []string{"github:repo:owner/repo"},
		CreatedAt:    now,
		ExpiresAt:    now.Add(8 * time.Hour),
		LastActivity: now,
		MintCount:    5,
	}
	data, err = json.Marshal(respOrig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var respGot SessionStatusResponse
	if err := json.Unmarshal(data, &respGot); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !respGot.Active {
		t.Error("expected Active = true")
	}
	if respGot.MintCount != 5 {
		t.Errorf("MintCount = %d, want 5", respGot.MintCount)
	}
	if len(respGot.Resources) != 1 || respGot.Resources[0] != "github:repo:owner/repo" {
		t.Errorf("Resources = %v, want [github:repo:owner/repo]", respGot.Resources)
	}
}
