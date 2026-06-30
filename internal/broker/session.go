package broker

import (
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
)

type Session struct {
	ID string

	AgentName string

	HostRepoPath string

	Resources []brokerapi.ResourceURI

	RunID   string
	TaskRef string

	BindSecretHash []byte

	CreatedAt time.Time

	ExpiresAt time.Time

	IdleTimeout time.Duration

	LastActivity time.Time

	MintCount int64

	Revoked bool

	PeerUID uint32
}

func (s *Session) IsExpired() bool {
	return !time.Now().Before(s.ExpiresAt)
}

func (s *Session) IsIdle() bool {
	if s.IdleTimeout <= 0 {
		return false
	}
	return !time.Now().Before(s.LastActivity.Add(s.IdleTimeout))
}

func (s *Session) IsActive() bool {
	return !s.Revoked && !s.IsExpired() && !s.IsIdle()
}
