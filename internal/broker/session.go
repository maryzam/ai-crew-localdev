package broker

import (
	"time"
)

// Session represents the broker-side state for an agent session.
//
// The broker creates one Session per "ai-agent run" invocation. The session
// tracks the agent identity, granted resources, and lifecycle timestamps.
// The binding secret is stored as a SHA-256 hash; see BindSecretHash.
type Session struct {
	// ID is a unique identifier for this session (e.g. a UUID).
	ID string

	// AgentName identifies which agent profile created the session.
	AgentName string

	// HostRepoPath is the absolute path to the repository on the host.
	HostRepoPath string

	// Resources is the parsed list of credential-generic resources granted
	// to this session. Populated from CreateSessionRequest.Resources.
	Resources []ResourceURI

	RunID   string
	TaskRef string

	// BindSecretHash is the SHA-256 hash of the session binding secret.
	//
	// We use plain SHA-256 (not argon2/bcrypt) because the secret is 32
	// bytes of CSPRNG output. Preimage resistance of SHA-256 is sufficient
	// for random secrets of this length — there is no benefit to a slow
	// hash when the input space is 2^256.
	BindSecretHash []byte

	// CreatedAt is the time the session was created.
	CreatedAt time.Time

	// ExpiresAt is the absolute deadline after which the session is expired.
	// Default: 8 hours after creation.
	ExpiresAt time.Time

	// IdleTimeout is the maximum duration of inactivity before the session
	// is considered idle and expired. Default: 1 hour.
	IdleTimeout time.Duration

	// LastActivity is the timestamp of the most recent credential mint for
	// this session. session_status calls are read-only and must not advance
	// this timestamp; doing so would let a polling client extend idle TTL
	// without performing any real work.
	LastActivity time.Time

	// MintCount tracks how many credentials have been minted in this
	// session.
	MintCount int64

	// Revoked indicates whether the session has been explicitly revoked.
	Revoked bool

	// PeerUID owns the session. revoke/status authorize against it instead of
	// the bind secret, so the secret need never be persisted outside the memfd.
	PeerUID uint32
}

// IsExpired reports whether the session has passed its absolute TTL.
// Equality is treated as expired (≥ ExpiresAt), so a session expires at the
// exact instant its TTL elapses rather than one nanosecond later.
func (s *Session) IsExpired() bool {
	return !time.Now().Before(s.ExpiresAt)
}

// IsIdle reports whether the session has been inactive longer than its
// idle timeout. Equality is treated as idle (≥ LastActivity + IdleTimeout).
func (s *Session) IsIdle() bool {
	if s.IdleTimeout <= 0 {
		return false
	}
	return !time.Now().Before(s.LastActivity.Add(s.IdleTimeout))
}

// IsActive reports whether the session is usable: not revoked, not expired,
// and not idle.
func (s *Session) IsActive() bool {
	return !s.Revoked && !s.IsExpired() && !s.IsIdle()
}
