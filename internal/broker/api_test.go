package broker

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTokenRequestRoundTrip(t *testing.T) {
	orig := TokenRequest{
		SessionID:   "sess-123",
		BindSecret:  []byte("secret-bytes"),
		Repo:        "owner/repo",
		Permissions: map[string]string{"contents": "read", "metadata": "read"},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got TokenRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SessionID != orig.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, orig.SessionID)
	}
	if string(got.BindSecret) != string(orig.BindSecret) {
		t.Errorf("BindSecret mismatch")
	}
	if got.Repo != orig.Repo {
		t.Errorf("Repo = %q, want %q", got.Repo, orig.Repo)
	}
	if len(got.Permissions) != len(orig.Permissions) {
		t.Errorf("Permissions length = %d, want %d", len(got.Permissions), len(orig.Permissions))
	}
	for k, v := range orig.Permissions {
		if got.Permissions[k] != v {
			t.Errorf("Permissions[%q] = %q, want %q", k, got.Permissions[k], v)
		}
	}
}

func TestTokenRequestOmitsEmptyPermissions(t *testing.T) {
	orig := TokenRequest{
		SessionID:  "sess-123",
		BindSecret: []byte("secret"),
		Repo:       "owner/repo",
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := raw["permissions"]; ok {
		t.Error("expected permissions to be omitted when nil")
	}
}

func TestTokenResponseRoundTrip(t *testing.T) {
	orig := TokenResponse{
		Token:     "ghs_abc123",
		ExpiresAt: time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC),
		Repo:      "owner/repo",
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got TokenResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Token != orig.Token {
		t.Errorf("Token = %q, want %q", got.Token, orig.Token)
	}
	if !got.ExpiresAt.Equal(orig.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, orig.ExpiresAt)
	}
	if got.Repo != orig.Repo {
		t.Errorf("Repo = %q, want %q", got.Repo, orig.Repo)
	}
}

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
	body, _ := json.Marshal(TokenRequest{
		SessionID:  "sess-1",
		BindSecret: []byte("s"),
		Repo:       "o/r",
	})
	orig := Request{
		Method: MethodMintToken,
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

	var inner TokenRequest
	if err := json.Unmarshal(got.Body, &inner); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if inner.SessionID != "sess-1" {
		t.Errorf("inner SessionID = %q, want %q", inner.SessionID, "sess-1")
	}
}

func TestResponseEnvelopeSuccess(t *testing.T) {
	body, _ := json.Marshal(TokenResponse{Token: "tok", Repo: "o/r", ExpiresAt: time.Now()})
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
		AgentName:            "claude",
		Repo:                 "owner/repo",
		HostRepoPath:         "/home/user/repo",
		RequestedPermissions: map[string]string{"contents": "write"},
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
	if got.Repo != orig.Repo {
		t.Errorf("Repo = %q, want %q", got.Repo, orig.Repo)
	}
	if got.HostRepoPath != orig.HostRepoPath {
		t.Errorf("HostRepoPath = %q, want %q", got.HostRepoPath, orig.HostRepoPath)
	}
	if got.RequestedPermissions["contents"] != "write" {
		t.Errorf("RequestedPermissions[contents] = %q, want %q", got.RequestedPermissions["contents"], "write")
	}
}

func TestCreateSessionResponseRoundTrip(t *testing.T) {
	orig := CreateSessionResponse{
		SessionID:   "sess-abc",
		BindSecret:  []byte("raw-secret-32-bytes-of-csprng!!"),
		ExpiresAt:   time.Date(2026, 3, 11, 20, 0, 0, 0, time.UTC),
		IdleTimeout: time.Hour,
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
		Active:          true,
		AgentName:       "claude",
		Repo:            "owner/repo",
		CreatedAt:       now,
		ExpiresAt:       now.Add(8 * time.Hour),
		LastActivity:    now,
		TokenMintsCount: 5,
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
	if respGot.TokenMintsCount != 5 {
		t.Errorf("TokenMintsCount = %d, want 5", respGot.TokenMintsCount)
	}
}
