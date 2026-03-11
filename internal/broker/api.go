// Package broker defines API contract types for the ai-agent broker daemon.
//
// The broker authorizes token mint requests over a Unix domain socket using
// JSON-encoded request/response envelopes. These types define the wire
// protocol; the broker daemon implementation lives in a separate package
// (phase 2).
package broker

import (
	"encoding/json"
	"fmt"
	"time"
)

// DurationString is a time.Duration that marshals to and from a JSON string
// using Go's duration format (e.g., "1h30m0s"). This keeps the public socket
// contract human-readable and consistent with the string durations used in
// policy files, avoiding the nanosecond integer that time.Duration produces
// by default.
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

// ---- Request envelope / response envelope -----------------------------------

// Request is the top-level envelope sent by a client over the Unix socket.
// Method selects the operation; Body carries the method-specific payload.
type Request struct {
	Method string          `json:"method"`
	Body   json.RawMessage `json:"body"`
}

// Broker methods.
const (
	MethodMintToken     = "mint_token"
	MethodCreateSession = "create_session"
	MethodRevokeSession = "revoke_session"
	MethodSessionStatus = "session_status"
)

// Response is the top-level envelope returned by the broker.
// Exactly one of Body or Error is set depending on OK.
type Response struct {
	OK    bool            `json:"ok"`
	Body  json.RawMessage `json:"body,omitempty"`
	Error *ErrorResponse  `json:"error,omitempty"`
}

// ---- Error codes ------------------------------------------------------------

// ErrorResponse carries a machine-readable Code and a human-readable Message.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Machine-readable error codes returned by the broker.
const (
	ErrCodeSessionNotFound   = "session_not_found"
	ErrCodeSessionExpired    = "session_expired"
	ErrCodeBindingMismatch   = "binding_mismatch"
	ErrCodeRepoNotAllowed    = "repo_not_allowed"
	ErrCodePermissionDenied  = "permission_denied"
	ErrCodeUIDMismatch       = "uid_mismatch"
	ErrCodeRateLimited       = "rate_limited"
	ErrCodeBrokerUnavailable = "broker_unavailable"
	ErrCodeUpstreamError     = "upstream_error"
)

// ---- mint_token -------------------------------------------------------------

// TokenRequest is the body for the "mint_token" method.
// The credential helper sends this to obtain a short-lived GitHub token
// scoped to the given repository and permission set.
type TokenRequest struct {
	SessionID   string            `json:"session_id"`
	BindSecret  []byte            `json:"bind_secret"`            // presented by helper, never stored
	Repo        string            `json:"repo"`                   // owner/repo
	Permissions map[string]string `json:"permissions,omitempty"`  // optional downscope
}

// TokenResponse is returned on a successful "mint_token" call.
type TokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Repo      string    `json:"repo"`
}

// ---- create_session ---------------------------------------------------------

// CreateSessionRequest is the body for the "create_session" method.
// The launcher (ai-agent run) sends this when starting a new agent session.
type CreateSessionRequest struct {
	AgentName            string            `json:"agent_name"`
	Repo                 string            `json:"repo"`                   // owner/repo
	HostRepoPath         string            `json:"host_repo_path"`         // absolute path on host
	RequestedPermissions map[string]string `json:"requested_permissions"`
}

// CreateSessionResponse is returned on a successful "create_session" call.
// The raw bind secret is returned exactly once; the broker stores only the hash.
type CreateSessionResponse struct {
	SessionID   string        `json:"session_id"`
	BindSecret  []byte        `json:"bind_secret"`   // raw bytes, returned once
	ExpiresAt   time.Time     `json:"expires_at"`
	IdleTimeout DurationString `json:"idle_timeout"` // JSON string, e.g. "1h0m0s"
}

// ---- revoke_session ---------------------------------------------------------

// RevokeSessionRequest is the body for the "revoke_session" method.
type RevokeSessionRequest struct {
	SessionID  string `json:"session_id"`
	BindSecret []byte `json:"bind_secret"`
}

// RevokeSessionResponse is returned on a successful "revoke_session" call.
type RevokeSessionResponse struct {
	Revoked bool `json:"revoked"`
}

// ---- session_status ---------------------------------------------------------

// SessionStatusRequest is the body for the "session_status" method.
type SessionStatusRequest struct {
	SessionID  string `json:"session_id"`
	BindSecret []byte `json:"bind_secret"`
}

// SessionStatusResponse describes the current state of a session.
type SessionStatusResponse struct {
	Active          bool      `json:"active"`
	AgentName       string    `json:"agent_name"`
	Repo            string    `json:"repo"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	LastActivity    time.Time `json:"last_activity"`
	TokenMintsCount int64     `json:"token_mints_count"`
}
