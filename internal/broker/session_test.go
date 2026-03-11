package broker

import (
	"testing"
	"time"
)

func TestIsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{
			name:      "not expired - future expiry",
			expiresAt: time.Now().Add(time.Hour),
			want:      false,
		},
		{
			name:      "expired - past expiry",
			expiresAt: time.Now().Add(-time.Second),
			want:      true,
		},
		{
			name:      "expired - far past",
			expiresAt: time.Now().Add(-24 * time.Hour),
			want:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{ExpiresAt: tt.expiresAt}
			if got := s.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsIdle(t *testing.T) {
	tests := []struct {
		name         string
		idleTimeout  time.Duration
		lastActivity time.Time
		want         bool
	}{
		{
			name:         "not idle - recent activity",
			idleTimeout:  time.Hour,
			lastActivity: time.Now().Add(-30 * time.Minute),
			want:         false,
		},
		{
			name:         "idle - activity too old",
			idleTimeout:  time.Hour,
			lastActivity: time.Now().Add(-2 * time.Hour),
			want:         true,
		},
		{
			name:         "not idle - zero timeout disables idle check",
			idleTimeout:  0,
			lastActivity: time.Now().Add(-24 * time.Hour),
			want:         false,
		},
		{
			name:         "not idle - negative timeout disables idle check",
			idleTimeout:  -time.Hour,
			lastActivity: time.Now().Add(-24 * time.Hour),
			want:         false,
		},
		{
			name:         "idle - exactly at boundary (just past)",
			idleTimeout:  time.Hour,
			lastActivity: time.Now().Add(-time.Hour - time.Second),
			want:         true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{
				IdleTimeout:  tt.idleTimeout,
				LastActivity: tt.lastActivity,
			}
			if got := s.IsIdle(); got != tt.want {
				t.Errorf("IsIdle() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsActive(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		session Session
		want    bool
	}{
		{
			name: "active session",
			session: Session{
				ExpiresAt:    now.Add(8 * time.Hour),
				IdleTimeout:  time.Hour,
				LastActivity: now,
				Revoked:      false,
			},
			want: true,
		},
		{
			name: "revoked session",
			session: Session{
				ExpiresAt:    now.Add(8 * time.Hour),
				IdleTimeout:  time.Hour,
				LastActivity: now,
				Revoked:      true,
			},
			want: false,
		},
		{
			name: "expired session",
			session: Session{
				ExpiresAt:    now.Add(-time.Second),
				IdleTimeout:  time.Hour,
				LastActivity: now,
				Revoked:      false,
			},
			want: false,
		},
		{
			name: "idle session",
			session: Session{
				ExpiresAt:    now.Add(8 * time.Hour),
				IdleTimeout:  time.Hour,
				LastActivity: now.Add(-2 * time.Hour),
				Revoked:      false,
			},
			want: false,
		},
		{
			name: "revoked and expired",
			session: Session{
				ExpiresAt:    now.Add(-time.Hour),
				IdleTimeout:  time.Hour,
				LastActivity: now.Add(-2 * time.Hour),
				Revoked:      true,
			},
			want: false,
		},
		{
			name: "active with zero idle timeout",
			session: Session{
				ExpiresAt:    now.Add(8 * time.Hour),
				IdleTimeout:  0,
				LastActivity: now.Add(-24 * time.Hour),
				Revoked:      false,
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &tt.session
			if got := s.IsActive(); got != tt.want {
				t.Errorf("IsActive() = %v, want %v", got, tt.want)
			}
		})
	}
}
