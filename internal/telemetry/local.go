package telemetry

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type localSink struct {
	mu   sync.Mutex
	path string
	file *os.File
	w    *bufio.Writer
}

func newLocalSink(path string) (*localSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("telemetry: create log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("telemetry: open %s: %w", path, err)
	}
	return &localSink{
		path: path,
		file: f,
		w:    bufio.NewWriter(f),
	}, nil
}

func (s *localSink) write(ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("telemetry: marshal event: %w", err)
	}
	if _, err := s.w.Write(data); err != nil {
		return fmt.Errorf("telemetry: write event: %w", err)
	}
	if err := s.w.WriteByte('\n'); err != nil {
		return fmt.Errorf("telemetry: write newline: %w", err)
	}
	return s.w.Flush()
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
