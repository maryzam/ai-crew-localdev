package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFileAuditLoggerPersistsBeforeRecordReturns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	event := AuditEvent{Timestamp: time.Now(), EventType: EventSessionCreated, SessionID: "session-1", Success: true}
	if err := logger.Record(event); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got AuditEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.EventType != event.EventType || got.SessionID != event.SessionID {
		t.Fatalf("event = %#v", got)
	}
}

func TestFileAuditLoggerSerializesConcurrentRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	const count = 64
	var wait sync.WaitGroup
	errs := make(chan error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errs <- logger.Record(AuditEvent{EventType: EventTokenMinted, SessionID: string(rune(index + 1))})
		}(index)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	seen := make(map[string]struct{}, count)
	for scanner.Scan() {
		var event AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		seen[event.SessionID] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != count {
		t.Fatalf("records = %d, want %d", len(seen), count)
	}
}

func TestFileAuditLoggerLatchesStorageFailure(t *testing.T) {
	logger, err := NewFileAuditLogger(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.file.Close(); err != nil {
		t.Fatal(err)
	}
	first := logger.Record(AuditEvent{EventType: EventTokenMinted})
	second := logger.Record(AuditEvent{EventType: EventTokenMinted})
	if !errors.Is(first, ErrAuditUnavailable) || !errors.Is(second, ErrAuditUnavailable) || !errors.Is(logger.Health(), ErrAuditUnavailable) {
		t.Fatalf("first=%v second=%v health=%v", first, second, logger.Health())
	}
}

func TestFileAuditLoggerRejectsUnsafeTargets(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "symlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileAuditLogger(filepath.Join(dir, "symlink")); err == nil {
		t.Fatal("symlink accepted")
	}
	insecure := filepath.Join(dir, "insecure")
	if err := os.WriteFile(insecure, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileAuditLogger(insecure); err == nil {
		t.Fatal("insecure mode accepted")
	}
}

func TestFileAuditLoggerRejectsRecordsAfterClose(t *testing.T) {
	logger, err := NewFileAuditLogger(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	if err := logger.Record(AuditEvent{}); !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("Record after Close = %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkFileAuditLoggerRecord(b *testing.B) {
	logger, err := NewFileAuditLogger(filepath.Join(b.TempDir(), "audit.log"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = logger.Close() })
	event := AuditEvent{EventType: EventTokenMinted, SessionID: "session", Success: true}
	b.ResetTimer()
	for range b.N {
		if err := logger.Record(event); err != nil {
			b.Fatal(err)
		}
	}
}
