package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

const defaultLocalTelemetryMaxBytes int64 = 10 * 1024 * 1024

type localSink struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	closed   bool
	metrics  *deliveryMetrics
}

func newLocalSink(path string) (*localSink, error) {
	return newLocalSinkMeasured(path, defaultLocalTelemetryMaxBytes, newDeliveryMetrics(DefaultDeliveryBudgets()))
}

func newLocalSinkSized(path string, maxBytes int64) (*localSink, error) {
	return newLocalSinkMeasured(path, maxBytes, newDeliveryMetrics(DefaultDeliveryBudgets()))
}

func newLocalSinkMeasured(path string, maxBytes int64, metrics *deliveryMetrics) (*localSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("telemetry: create log dir: %w", err)
	}
	sink := &localSink{path: path, maxBytes: maxBytes, metrics: metrics}
	if err := sink.withFileLock(func() error {
		file, err := openTelemetryFile(path)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if err := rotateExisting(path, maxBytes); err != nil {
			return err
		}
		file, err = openTelemetryFile(path)
		if err != nil {
			return err
		}
		return file.Close()
	}); err != nil {
		return nil, err
	}
	return sink, nil
}

func (s *localSink) write(event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		s.metrics.rejected(1)
		return fmt.Errorf("telemetry: marshal event: %w", err)
	}
	data = append(data, '\n')
	s.metrics.payload(len(data))
	started := s.metrics.started()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		s.metrics.rejected(1)
		return fmt.Errorf("telemetry: write after close")
	}
	err = s.withFileLock(func() error {
		info, err := os.Stat(s.path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("telemetry: stat %s: %w", s.path, err)
		}
		if err == nil && s.maxBytes > 0 && info.Size() > 0 && info.Size()+int64(len(data)) > s.maxBytes {
			if err := rotatePath(s.path); err != nil {
				return err
			}
		}

		file, err := openTelemetryFile(s.path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		if _, err := file.Write(data); err != nil {
			return fmt.Errorf("telemetry: write event: %w", err)
		}
		return nil
	})
	s.metrics.wroteLocal(started)
	if err != nil {
		s.metrics.rejected(1)
	}
	return err
}

func (s *localSink) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *localSink) withFileLock(action func() error) error {
	lockPath := s.path + ".lock"
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("telemetry: open lock: %w", err)
	}
	lock := os.NewFile(uintptr(fd), lockPath)
	if lock == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("telemetry: open lock: invalid file descriptor")
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Fchmod(fd, 0o600); err != nil {
		return fmt.Errorf("telemetry: secure lock: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("telemetry: acquire lock: %w", err)
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	return action()
}

func openTelemetryFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_WRONLY|unix.O_APPEND|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("telemetry: open %s: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("telemetry: open %s: invalid file descriptor", path)
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("telemetry: secure %s: %w", path, err)
	}
	return file, nil
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
