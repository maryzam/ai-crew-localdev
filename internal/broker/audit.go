package broker

import "time"

// AuditEvent records a single auditable action performed by the broker.
// Events are emitted for session lifecycle changes and token operations.
type AuditEvent struct {
	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`

	// EventType identifies the kind of event (see Event* constants).
	EventType string `json:"event_type"`

	// SessionID is the session this event relates to.
	SessionID string `json:"session_id"`

	// AgentName identifies the agent profile involved.
	AgentName string `json:"agent_name"`

	// Repo is the owner/repo, if applicable.
	Repo string `json:"repo,omitempty"`

	// PeerUID is the Unix UID of the peer process (from SO_PEERCRED).
	PeerUID uint32 `json:"peer_uid"`

	// Success indicates whether the operation succeeded.
	Success bool `json:"success"`

	// ErrorCode is the machine-readable error code on failure.
	ErrorCode string `json:"error_code,omitempty"`

	// ErrorDetail provides additional context on failure.
	ErrorDetail string `json:"error_detail,omitempty"`

	// DurationMS is the wall-clock duration of the operation in milliseconds.
	DurationMS int64 `json:"duration_ms"`

	// Metadata carries optional key-value pairs for extensibility.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Audit event types.
const (
	EventSessionCreated = "session.created"
	EventSessionRevoked = "session.revoked"
	EventSessionExpired = "session.expired"
	EventTokenMinted    = "token.minted"
	EventTokenDenied    = "token.denied"
	EventTokenCacheHit  = "token.cache_hit"
	EventBindingFailed  = "token.binding_failed"
	EventUIDMismatch    = "token.uid_mismatch"
)

// AuditLogger is the interface for emitting audit events.
//
// Implementations may write to structured log files, forward to a logging
// service, or aggregate events for later retrieval. Implementations must
// be safe for concurrent use.
type AuditLogger interface {
	// Log records a single audit event. Implementations should not block
	// on I/O in the hot path; buffering is encouraged.
	Log(event AuditEvent)

	// Close flushes any buffered events and releases resources.
	Close() error
}
