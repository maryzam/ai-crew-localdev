package telemetry

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const defaultLocalTelemetryMaxBytes int64 = 10 * 1024 * 1024

type localSink struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	w        *bufio.Writer
	size     int64
	maxBytes int64
}

func newLocalSink(path string) (*localSink, error) {
	return newLocalSinkSized(path, defaultLocalTelemetryMaxBytes)
}

func newLocalSinkSized(path string, maxBytes int64) (*localSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("telemetry: create log dir: %w", err)
	}
	if err := rotateExisting(path, maxBytes); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("telemetry: open %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("telemetry: stat %s: %w", path, err)
	}
	return &localSink{
		path:     path,
		file:     f,
		w:        bufio.NewWriter(f),
		size:     info.Size(),
		maxBytes: maxBytes,
	}, nil
}

func (s *localSink) write(ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("telemetry: marshal event: %w", err)
	}
	recordSize := int64(len(data) + 1)
	if s.maxBytes > 0 && s.size > 0 && s.size+recordSize > s.maxBytes {
		if err := s.rotateLocked(); err != nil {
			return err
		}
	}
	if _, err := s.w.Write(data); err != nil {
		return fmt.Errorf("telemetry: write event: %w", err)
	}
	if err := s.w.WriteByte('\n'); err != nil {
		return fmt.Errorf("telemetry: write newline: %w", err)
	}
	if err := s.w.Flush(); err != nil {
		return err
	}
	s.size += recordSize
	return nil
}

func (s *localSink) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var flushErr error
	if s.w != nil {
		flushErr = s.w.Flush()
	}
	closeErr := s.file.Close()
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}

func (s *localSink) rotateLocked() error {
	if s.w != nil {
		if err := s.w.Flush(); err != nil {
			return err
		}
	}
	if err := s.file.Close(); err != nil {
		return err
	}
	if err := rotatePath(s.path); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("telemetry: open rotated %s: %w", s.path, err)
	}
	s.file = f
	s.w = bufio.NewWriter(f)
	s.size = 0
	return nil
}

func rotateExisting(path string, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	info, err := os.Stat(path)
	if err == nil && info.Size() >= maxBytes {
		return rotatePath(path)
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("telemetry: stat %s: %w", path, err)
	}
	return nil
}

func rotatePath(path string) error {
	backup := path + ".1"
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("telemetry: remove %s: %w", backup, err)
	}
	if err := os.Rename(path, backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("telemetry: rotate %s: %w", path, err)
	}
	return nil
}
