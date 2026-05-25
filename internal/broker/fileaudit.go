package broker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

const (
	auditChanSize  = 1024
	auditFlushFreq = 1 * time.Second
)

// AuditEvent records a single auditable action performed by the broker.
// Events are emitted for session lifecycle changes and token operations.
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

// FileAuditLogger writes audit events as JSON lines to a file.
// Writes are buffered and flushed periodically to avoid blocking
// the broker's hot path.
type FileAuditLogger struct {
	ch     chan AuditEvent
	done   chan struct{}
	closed sync.Once
}

// NewFileAuditLogger opens (or creates) the audit log file and starts
// the background writer goroutine. The file is opened in append mode
// with permissions 0600.
func NewFileAuditLogger(path string) (*FileAuditLogger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}

	l := &FileAuditLogger{
		ch:   make(chan AuditEvent, auditChanSize),
		done: make(chan struct{}),
	}

	go l.writer(f)
	return l, nil
}

// Log enqueues an audit event for writing. If the buffer is full, the
// event is dropped silently to avoid blocking the broker.
func (l *FileAuditLogger) Log(event AuditEvent) {
	select {
	case l.ch <- event:
	default:
		// Buffer full; drop to avoid blocking the broker hot path.
		fmt.Fprintf(os.Stderr, "audit: buffer full, dropping event %s\n", event.EventType)
	}
}

// Close drains the event buffer, flushes to disk, and closes the file.
func (l *FileAuditLogger) Close() error {
	l.closed.Do(func() {
		close(l.ch)
	})
	<-l.done
	return nil
}

func (l *FileAuditLogger) writer(f *os.File) {
	defer close(l.done)
	defer func() { _ = f.Close() }()

	w := bufio.NewWriter(f)
	ticker := time.NewTicker(auditFlushFreq)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-l.ch:
			if !ok {
				// Channel closed; drain remaining events.
				for evt := range l.ch {
					writeEvent(w, evt)
				}
				_ = w.Flush()
				return
			}
			writeEvent(w, event)
		case <-ticker.C:
			_ = w.Flush()
		}
	}
}

func writeEvent(w *bufio.Writer, event AuditEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit: marshal error: %v\n", err)
		return
	}
	_, _ = w.Write(data)
	_ = w.WriteByte('\n')
}
