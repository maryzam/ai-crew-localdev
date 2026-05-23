// Package broker defines API contract types for the ai-agent broker daemon.
//
// The broker authorizes credential mint requests over a Unix domain socket
// using JSON-encoded request/response envelopes.
//
// This file is mid-refactor toward a credential-type-generic API. See
// docs/decisions/0001-credential-generic-broker-api.md. During the migration,
// the legacy GitHub-shaped types (TokenRequest, TokenResponse, MethodMintToken,
// CreateSessionRequest.Repo) coexist with the new credential-generic types
// (CredentialRequest, CredentialResponse, MethodMintCredential, ResourceURI).
// The legacy surface will be removed once all callers migrate.
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

// Broker methods. MethodMintToken is legacy and will be removed; new clients
// should use MethodMintCredential.
const (
	MethodMintToken      = "mint_token"
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
	ErrCodeRepoNotAllowed     = "repo_not_allowed"     // legacy; resource_not_allowed in new API
	ErrCodeResourceNotAllowed = "resource_not_allowed"
	ErrCodePermissionDenied   = "permission_denied"
	ErrCodeUIDMismatch        = "uid_mismatch"
	ErrCodeRateLimited        = "rate_limited"
	ErrCodeBrokerUnavailable  = "broker_unavailable"
	ErrCodeUpstreamError      = "upstream_error"
	ErrCodeUnknownCredType    = "unknown_credential_type"
	ErrCodeInvalidResourceURI = "invalid_resource_uri"
)

// ---- Credential types -------------------------------------------------------

const (
	CredentialTypeGitHubAppInstallation = "github_app_installation"
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

// ---- mint_token (legacy) ----------------------------------------------------

// TokenRequest is the body for the legacy "mint_token" method. New callers
// should use CredentialRequest with MethodMintCredential.
type TokenRequest struct {
	SessionID   string            `json:"session_id"`
	BindSecret  []byte            `json:"bind_secret"`
	Repo        string            `json:"repo"`
	Permissions map[string]string `json:"permissions,omitempty"`
}

// TokenResponse is returned by the legacy "mint_token" method.
type TokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Repo      string    `json:"repo"`
}

// ---- mint_credential (new, credential-generic) ------------------------------

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

// GitHubProviderConfig is the per-agent GitHub provider configuration the
// broker extracts from policy and passes to the GitHub provider via
// ProviderMintRequest.ProviderConfig. It lives in the broker package so
// broker code can build it without importing the provider, breaking what
// would otherwise be an import cycle.
type GitHubProviderConfig struct {
	InstallationID     int64             `json:"installation_id"`
	AppID              string            `json:"app_id"`
	DefaultPermissions map[string]string `json:"default_permissions,omitempty"`
}

// ---- create_session ---------------------------------------------------------

// CreateSessionRequest is the body for the "create_session" method.
//
// Migration state: Repo + RequestedPermissions are legacy single-resource
// fields. Resources is the credential-generic replacement. Callers should
// fill Resources; the broker accepts either form during migration.
type CreateSessionRequest struct {
	AgentName            string            `json:"agent_name"`
	HostRepoPath         string            `json:"host_repo_path"`
	Repo                 string            `json:"repo,omitempty"`                  // legacy
	RequestedPermissions map[string]string `json:"requested_permissions,omitempty"` // legacy
	Resources            []string          `json:"resources,omitempty"`             // new
}

type CreateSessionResponse struct {
	SessionID   string         `json:"session_id"`
	BindSecret  []byte         `json:"bind_secret"`
	ExpiresAt   time.Time      `json:"expires_at"`
	IdleTimeout DurationString `json:"idle_timeout"`
}

// ---- revoke_session ---------------------------------------------------------

type RevokeSessionRequest struct {
	SessionID  string `json:"session_id"`
	BindSecret []byte `json:"bind_secret"`
}

type RevokeSessionResponse struct {
	Revoked bool `json:"revoked"`
}

// ---- session_status ---------------------------------------------------------

type SessionStatusRequest struct {
	SessionID  string `json:"session_id"`
	BindSecret []byte `json:"bind_secret"`
}

type SessionStatusResponse struct {
	Active          bool      `json:"active"`
	AgentName       string    `json:"agent_name"`
	Repo            string    `json:"repo"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	LastActivity    time.Time `json:"last_activity"`
	TokenMintsCount int64     `json:"token_mints_count"`
}

// ---- health_check -----------------------------------------------------------

type HealthCheckRequest struct{}

type HealthCheckResponse struct {
	Healthy bool `json:"healthy"`
}
