package core

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/platform/correlation"
)

const (
	DefaultSessionTTL = 8 * time.Hour

	DefaultIdleTimeout = 1 * time.Hour

	bindSecretLen = 32
)

type MemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session

	SessionTTL  time.Duration
	IdleTimeout time.Duration
}

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

func (s *MemorySessionStore) Create(req api.CreateSessionRequest, peerUID uint32) (*Session, []byte, error) {
	if err := correlation.ValidateRunID(req.RunID); err != nil {
		return nil, nil, err
	}
	if err := correlation.ValidateTaskRef(req.TaskRef); err != nil {
		return nil, nil, err
	}
	if len(req.Resources) == 0 {
		return nil, nil, fmt.Errorf("create session: resources must not be empty")
	}
	resources := make([]api.ResourceURI, 0, len(req.Resources))
	for _, raw := range req.Resources {
		r, err := api.ParseResourceURI(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("create session: %w", err)
		}
		resources = append(resources, r)
	}

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
		HostRepoPath:   req.HostRepoPath,
		Resources:      resources,
		RunID:          req.RunID,
		TaskRef:        req.TaskRef,
		BindSecretHash: append([]byte(nil), hash[:]...),
		CreatedAt:      now,
		ExpiresAt:      now.Add(s.sessionTTL()),
		IdleTimeout:    s.idleTimeout(),
		LastActivity:   now,
		PeerUID:        peerUID,
	}

	s.mu.Lock()
	s.sessions[id] = session
	s.mu.Unlock()

	return cloneSession(session), secret, nil
}

func (s *MemorySessionStore) Get(sessionID string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	return cloneSession(session), nil
}

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

func (s *MemorySessionStore) RecordActivity(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %q not found", sessionID)
	}
	session.LastActivity = time.Now()
	session.MintCount++
	return nil
}

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

func (s *MemorySessionStore) Cleanup(allow func(*Session) bool) []*Session {
	s.mu.RLock()
	var candidates []*Session
	for _, session := range s.sessions {
		if !session.IsActive() {
			candidates = append(candidates, cloneSession(session))
		}
	}
	s.mu.RUnlock()
	var expired []*Session
	for _, candidate := range candidates {
		if !allow(candidate) {
			continue
		}
		s.mu.Lock()
		current, exists := s.sessions[candidate.ID]
		if exists && !current.IsActive() {
			if !current.Revoked {
				expired = append(expired, cloneSession(current))
			}
			delete(s.sessions, candidate.ID)
		}
		s.mu.Unlock()
	}
	return expired
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
	cloned.BindSecretHash = append([]byte(nil), session.BindSecretHash...)
	if len(session.Resources) > 0 {
		cloned.Resources = append([]api.ResourceURI(nil), session.Resources...)
	} else {
		cloned.Resources = nil
	}
	return &cloned
}
