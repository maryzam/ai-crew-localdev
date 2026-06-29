// Package broker defines API contract types for the ai-agent broker daemon.
//
// The broker authorizes credential mint requests over a Unix domain socket
// using JSON-encoded request/response envelopes. The API is credential-
// generic: a single mint_credential method dispatches on a credential_type
// discriminator and a resource URI. See
// docs/decisions/0001-credential-generic-broker-api.md.
package broker

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DurationString is a time.Duration that marshals to and from a JSON string
// using Go's duration format (e.g., "1h30m0s").
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

// ---- Request / Response envelopes -------------------------------------------

type Request struct {
	Method string          `json:"method"`
	Body   json.RawMessage `json:"body"`
}

// Broker methods.
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

// ---- Error codes ------------------------------------------------------------

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

// ---- Credential types -------------------------------------------------------

const (
	CredentialTypeGitHubAppInstallation = "github_app_installation"
	CredentialTypeLangfuseOTLP          = "langfuse_otlp"
)

// ---- Resource URIs ----------------------------------------------------------

// ResourceURI is a parsed <provider>:<kind>:<identifier> resource locator.
// The identifier may itself contain colons (e.g. AWS ARNs); parsing splits
// on the first two colons only.
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

// ---- mint_credential --------------------------------------------------------

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

// GitHubAppInstallationCredential is the credential payload for
// credential_type == CredentialTypeGitHubAppInstallation.
type GitHubAppInstallationCredential struct {
	Token string `json:"token"`
}

// GitHubAppInstallationParams is the params payload for
// credential_type == CredentialTypeGitHubAppInstallation.
type GitHubAppInstallationParams struct {
	Permissions map[string]string `json:"permissions,omitempty"`
}

// LangfuseOTLPCredential is returned only to the trusted launcher. The agent
// receives a short-lived token for the launcher's local relay instead.
type LangfuseOTLPCredential struct {
	Endpoint  string `json:"endpoint"`
	PublicKey string `json:"public_key"`
	SecretKey string `json:"secret_key"`
}

// ---- create_session ---------------------------------------------------------

// CreateSessionRequest is the body for the "create_session" method.
// Sessions are scoped to one or more resource URIs.
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

// ---- revoke_session ---------------------------------------------------------

type RevokeSessionRequest struct {
	SessionID string `json:"session_id"`

	// Deprecated: revocation is authorized by the connecting process's UID
	// (SO_PEERCRED) matching the session owner, so the bind secret no longer
	// needs to be presented or persisted. Retained for wire compatibility;
	// the broker ignores it.
	BindSecret []byte `json:"bind_secret,omitempty"`
}

type RevokeSessionResponse struct {
	Revoked bool `json:"revoked"`
}

// ---- session_status ---------------------------------------------------------

type SessionStatusRequest struct {
	SessionID string `json:"session_id"`

	// Deprecated: status is authorized by the connecting process's UID
	// (SO_PEERCRED) matching the session owner. Retained for wire
	// compatibility; the broker ignores it.
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

// ---- health_check -----------------------------------------------------------

type HealthCheckRequest struct{}

type HealthCheckResponse struct {
	Healthy bool `json:"healthy"`
}
