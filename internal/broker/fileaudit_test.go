package broker

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileAuditLoggerWritesJSONLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")

	logger, err := NewFileAuditLogger(logPath)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	events := []AuditEvent{
		{
			Timestamp: time.Now(),
			EventType: EventSessionCreated,
			SessionID: "sess-1",
			AgentName: "claude",
			Repo:      "o/r",
			PeerUID:   1000,
			Success:   true,
		},
		{
			Timestamp: time.Now(),
			EventType: EventTokenMinted,
			SessionID: "sess-1",
			AgentName: "claude",
			Repo:      "o/r",
			PeerUID:   1000,
			Success:   true,
		},
		{
			Timestamp:   time.Now(),
			EventType:   EventTokenDenied,
			SessionID:   "sess-2",
			AgentName:   "codex",
			Repo:        "o/r2",
			PeerUID:     1000,
			Success:     false,
			ErrorCode:   ErrCodeRepoNotAllowed,
			ErrorDetail: "repo not in allowed list",
		},
	}

	for _, e := range events {
		logger.Log(e)
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read back and verify.
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	var read []AuditEvent
	for scanner.Scan() {
		var e AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal line: %v", err)
		}
		read = append(read, e)
	}

	if len(read) != len(events) {
		t.Fatalf("read %d events, want %d", len(read), len(events))
	}

	if read[0].EventType != EventSessionCreated {
		t.Errorf("event[0] type = %q, want %q", read[0].EventType, EventSessionCreated)
	}
	if read[2].ErrorCode != ErrCodeRepoNotAllowed {
		t.Errorf("event[2] error_code = %q, want %q", read[2].ErrorCode, ErrCodeRepoNotAllowed)
	}
}

func TestFileAuditLoggerAppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")

	// Write first event.
	logger1, _ := NewFileAuditLogger(logPath)
	logger1.Log(AuditEvent{EventType: "first"})
	_ = logger1.Close()

	// Write second event.
	logger2, _ := NewFileAuditLogger(logPath)
	logger2.Log(AuditEvent{EventType: "second"})
	_ = logger2.Close()

	// Verify both events.
	f, _ := os.Open(logPath)
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	var count int
	for scanner.Scan() {
		count++
	}
	if count != 2 {
		t.Errorf("line count = %d, want 2", count)
	}
}
