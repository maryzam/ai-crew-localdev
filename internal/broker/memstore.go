package broker

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultSessionTTL is the default absolute session lifetime.
	DefaultSessionTTL = 8 * time.Hour

	// DefaultIdleTimeout is the default inactivity timeout.
	DefaultIdleTimeout = 1 * time.Hour

	// bindSecretLen is the length of the CSPRNG session binding secret.
	bindSecretLen = 32
)

// MemorySessionStore is an in-memory implementation of SessionStore.
type MemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session

	// Configurable defaults; zero values use package defaults.
	SessionTTL  time.Duration
	IdleTimeout time.Duration
}

// NewMemorySessionStore creates a new in-memory session store.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{
		sessions: make(map[string]*Session),
	}
}

func (s *MemorySessionStore) sessionTTL() time.Duration {
	if s.SessionTTL > 0 {
		return s.SessionTTL
	}
	return DefaultSessionTTL
}

func (s *MemorySessionStore) idleTimeout() time.Duration {
	if s.IdleTimeout > 0 {
		return s.IdleTimeout
	}
	return DefaultIdleTimeout
}

// Create allocates a new session and returns it along with the raw bind secret.
func (s *MemorySessionStore) Create(req CreateSessionRequest, peerUID uint32) (*Session, []byte, error) {
	secret := make([]byte, bindSecretLen)
	if _, err := rand.Read(secret); err != nil {
		return nil, nil, fmt.Errorf("generate bind secret: %w", err)
	}

	id, err := generateSessionID()
	if err != nil {
		return nil, nil, fmt.Errorf("generate session ID: %w", err)
	}

	hash := sha256.Sum256(secret)
	now := time.Now()

	session := &Session{
		ID:             id,
		AgentName:      req.AgentName,
		Repo:           req.Repo,
		HostRepoPath:   req.HostRepoPath,
		Permissions:    copyPermissions(req.RequestedPermissions),
		BindSecretHash: append([]byte(nil), hash[:]...),
		CreatedAt:      now,
		ExpiresAt:      now.Add(s.sessionTTL()),
		IdleTimeout:    s.idleTimeout(),
		LastActivity:   now,
	}

	s.mu.Lock()
	s.sessions[id] = session
	s.mu.Unlock()

	return cloneSession(session), secret, nil
}

// Get retrieves a session by ID.
func (s *MemorySessionStore) Get(sessionID string) (*Session, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	return cloneSession(session), nil
}

// ValidateBinding verifies the bind secret against the stored hash using
// constant-time comparison.
func (s *MemorySessionStore) ValidateBinding(sessionID string, bindSecret []byte) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	if !ok {
		s.mu.RUnlock()
		return fmt.Errorf("session %q not found", sessionID)
	}
	hashCopy := append([]byte(nil), session.BindSecretHash...)
	s.mu.RUnlock()

	hash := sha256.Sum256(bindSecret)
	if subtle.ConstantTimeCompare(hash[:], hashCopy) != 1 {
		return fmt.Errorf("binding mismatch for session %q", sessionID)
	}
	return nil
}

// RecordActivity updates LastActivity and increments TokenMintCount.
func (s *MemorySessionStore) RecordActivity(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %q not found", sessionID)
	}
	session.LastActivity = time.Now()
	session.TokenMintCount++
	return nil
}

// Revoke marks a session as revoked.
func (s *MemorySessionStore) Revoke(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %q not found", sessionID)
	}
	session.Revoked = true
	return nil
}

// Cleanup removes expired, idle, and revoked sessions.
func (s *MemorySessionStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, session := range s.sessions {
		if !session.IsActive() {
			delete(s.sessions, id)
		}
	}
}

func generateSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func cloneSession(session *Session) *Session {
	if session == nil {
		return nil
	}

	cloned := *session
	cloned.Permissions = copyPermissions(session.Permissions)
	cloned.BindSecretHash = append([]byte(nil), session.BindSecretHash...)
	return &cloned
}

func copyPermissions(perms map[string]string) map[string]string {
	if len(perms) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(perms))
	for key, value := range perms {
		cloned[key] = value
	}
	return cloned
}
