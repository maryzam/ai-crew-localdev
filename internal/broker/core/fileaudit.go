package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
	"golang.org/x/sys/unix"
)

var ErrAuditUnavailable = errors.New("audit unavailable")

const (
	EventSessionCreated            = "session.created"
	EventSessionRevokeRequested    = "session.revoke_requested"
	EventSessionRevoked            = "session.revoked"
	EventSessionExpireRequested    = "session.expire_requested"
	EventSessionExpired            = "session.expired"
	EventResourcesAuthorized       = "resources.authorized"
	EventResourcesDenied           = "resources.denied"
	EventTokenMintRequested        = "token.mint_requested"
	EventTokenMinted               = "token.minted"
	EventTokenDenied               = "token.denied"
	EventTokenCacheHit             = "token.cache_hit"
	EventBindingFailed             = "token.binding_failed"
	EventUIDMismatch               = "token.uid_mismatch"
	EventTelemetryPublishRequested = "telemetry.publish_requested"
	EventTelemetryPublished        = "telemetry.published"
	EventTelemetryDenied           = "telemetry.denied"
)

type AuditEvent struct {
	Timestamp   time.Time         `json:"timestamp"`
	EventType   string            `json:"event_type"`
	SessionID   string            `json:"session_id"`
	AgentName   string            `json:"agent_name"`
	Repo        string            `json:"repo,omitempty"`
	PeerUID     uint32            `json:"peer_uid"`
	Success     bool              `json:"success"`
	ErrorCode   string            `json:"error_code,omitempty"`
	ErrorDetail string            `json:"error_detail,omitempty"`
	DurationMS  int64             `json:"duration_ms"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type AuditSink interface {
	Record(AuditEvent) error
	Health() error
}

type FileAuditLogger struct {
	mu      sync.Mutex
	file    *os.File
	failure error
	closed  bool
}

func NewFileAuditLogger(path string) (*FileAuditLogger, error) {
	fd, err := unix.Open(path, unix.O_APPEND|unix.O_CREAT|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("audit: open %s", path)
	}
	if err := validateAuditFile(fd); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	if err := securefile.SyncDirectory(filepath.Dir(path)); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("audit: sync directory: %w", err)
	}
	return &FileAuditLogger{file: file}, nil
}

func validateAuditFile(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("audit path must be a regular file")
	}
	if stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("audit file owner does not match broker user")
	}
	if stat.Mode&0o077 != 0 {
		return fmt.Errorf("audit file must be owner-only")
	}
	return nil
}

func (l *FileAuditLogger) Record(event AuditEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.healthLocked(); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return l.failLocked(err)
	}
	data = append(data, '\n')
	written, err := l.file.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = l.file.Sync()
	}
	if err != nil {
		return l.failLocked(err)
	}
	return nil
}

func (l *FileAuditLogger) Health() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.healthLocked()
}

func (l *FileAuditLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.failure
	}
	l.closed = true
	err := l.file.Sync()
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return l.failLocked(err)
	}
	return l.failure
}

func (l *FileAuditLogger) healthLocked() error {
	if l.failure != nil {
		return l.failure
	}
	if l.closed {
		return fmt.Errorf("%w: logger closed", ErrAuditUnavailable)
	}
	return nil
}

func (l *FileAuditLogger) failLocked(err error) error {
	if l.failure == nil {
		l.failure = fmt.Errorf("%w: %v", ErrAuditUnavailable, err)
	}
	return l.failure
}
