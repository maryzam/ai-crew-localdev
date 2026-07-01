package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type DurationString time.Duration

func (d DurationString) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *DurationString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("DurationString: expected a JSON string, got %s", b)
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("DurationString: %w", err)
	}
	*d = DurationString(dur)
	return nil
}

type Request struct {
	Method string          `json:"method"`
	Body   json.RawMessage `json:"body"`
}

const (
	MethodMintCredential = "mint_credential"
	MethodCreateSession  = "create_session"
	MethodRevokeSession  = "revoke_session"
	MethodSessionStatus  = "session_status"
	MethodHealthCheck    = "health_check"
)

type Response struct {
	OK    bool            `json:"ok"`
	Body  json.RawMessage `json:"body,omitempty"`
	Error *ErrorResponse  `json:"error,omitempty"`
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	ErrCodeSessionNotFound    = "session_not_found"
	ErrCodeSessionExpired     = "session_expired"
	ErrCodeBindingMismatch    = "binding_mismatch"
	ErrCodeResourceNotAllowed = "resource_not_allowed"
	ErrCodePermissionDenied   = "permission_denied"
	ErrCodeUIDMismatch        = "uid_mismatch"
	ErrCodeRateLimited        = "rate_limited"
	ErrCodeBrokerUnavailable  = "broker_unavailable"
	ErrCodeUpstreamError      = "upstream_error"
	ErrCodeUnknownCredType    = "unknown_credential_type"
	ErrCodeInvalidResourceURI = "invalid_resource_uri"
	ErrCodeInvalidCorrelation = "invalid_correlation"
)

type ResourceURI struct {
	Provider   string
	Kind       string
	Identifier string
}

func ParseResourceURI(s string) (ResourceURI, error) {
	first := strings.IndexByte(s, ':')
	if first <= 0 {
		return ResourceURI{}, fmt.Errorf("resource URI %q: missing provider", s)
	}
	rest := s[first+1:]
	second := strings.IndexByte(rest, ':')
	if second <= 0 {
		return ResourceURI{}, fmt.Errorf("resource URI %q: missing kind", s)
	}
	id := rest[second+1:]
	if id == "" {
		return ResourceURI{}, fmt.Errorf("resource URI %q: missing identifier", s)
	}
	return ResourceURI{
		Provider:   s[:first],
		Kind:       rest[:second],
		Identifier: id,
	}, nil
}

func (r ResourceURI) String() string {
	return r.Provider + ":" + r.Kind + ":" + r.Identifier
}

type CredentialRequest struct {
	SessionID      string          `json:"session_id"`
	BindSecret     []byte          `json:"bind_secret"`
	CredentialType string          `json:"credential_type"`
	Resource       string          `json:"resource"`
	Params         json.RawMessage `json:"params,omitempty"`
}

type CredentialResponse struct {
	CredentialType string          `json:"credential_type"`
	Resource       string          `json:"resource"`
	Credential     json.RawMessage `json:"credential"`
	ExpiresAt      time.Time       `json:"expires_at"`
}

type CreateSessionRequest struct {
	AgentName    string   `json:"agent_name"`
	HostRepoPath string   `json:"host_repo_path"`
	Resources    []string `json:"resources"`
	RunID        string   `json:"run_id,omitempty"`
	TaskRef      string   `json:"task_ref,omitempty"`
}

type CreateSessionResponse struct {
	SessionID   string         `json:"session_id"`
	BindSecret  []byte         `json:"bind_secret"`
	ExpiresAt   time.Time      `json:"expires_at"`
	IdleTimeout DurationString `json:"idle_timeout"`
}

type RevokeSessionRequest struct {
	SessionID string `json:"session_id"`

	BindSecret []byte `json:"bind_secret,omitempty"`
}

type RevokeSessionResponse struct {
	Revoked bool `json:"revoked"`
}

type SessionStatusRequest struct {
	SessionID string `json:"session_id"`

	BindSecret []byte `json:"bind_secret,omitempty"`
}

type SessionStatusResponse struct {
	Active       bool      `json:"active"`
	AgentName    string    `json:"agent_name"`
	Resources    []string  `json:"resources"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	LastActivity time.Time `json:"last_activity"`
	MintCount    int64     `json:"mint_count"`
}

type HealthCheckRequest struct{}

type HealthCheckResponse struct {
	Healthy bool `json:"healthy"`
}
