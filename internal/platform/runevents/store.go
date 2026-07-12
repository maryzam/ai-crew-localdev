package runevents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

const DefaultLocalMaxBytes int64 = 10 * 1024 * 1024

type Store struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	closed   bool
}

func OpenStore(path string) (*Store, error) {
	return OpenStoreSized(path, DefaultLocalMaxBytes)
}

func OpenStoreSized(path string, maxBytes int64) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("run events: create log dir: %w", err)
	}
	store := &Store{path: path, maxBytes: maxBytes}
	if err := store.withFileLock(func() error {
		file, err := openFile(path)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if err := rotateExisting(path, maxBytes); err != nil {
			return err
		}
		file, err = openFile(path)
		if err != nil {
			return err
		}
		return file.Close()
	}); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Write(event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("run events: marshal event: %w", err)
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("run events: write after close")
	}
	return s.withFileLock(func() error {
		info, err := os.Stat(s.path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("run events: stat %s: %w", s.path, err)
		}
		if err == nil && s.maxBytes > 0 && info.Size() > 0 && info.Size()+int64(len(data)) > s.maxBytes {
			if err := rotatePath(s.path); err != nil {
				return err
			}
		}

		file, err := openFile(s.path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		if _, err := file.Write(data); err != nil {
			return fmt.Errorf("run events: write event: %w", err)
		}
		return nil
	})
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *Store) withFileLock(action func() error) error {
	lockPath := s.path + ".lock"
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("run events: open lock: %w", err)
	}
	lock := os.NewFile(uintptr(fd), lockPath)
	if lock == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("run events: open lock: invalid file descriptor")
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Fchmod(fd, 0o600); err != nil {
		return fmt.Errorf("run events: secure lock: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("run events: acquire lock: %w", err)
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	return action()
}

func openFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_WRONLY|unix.O_APPEND|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("run events: open %s: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("run events: open %s: invalid file descriptor", path)
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("run events: secure %s: %w", path, err)
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
		return fmt.Errorf("run events: stat %s: %w", path, err)
	}
	return nil
}

func rotatePath(path string) error {
	backup := path + ".1"
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("run events: remove %s: %w", backup, err)
	}
	if err := os.Rename(path, backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("run events: rotate %s: %w", path, err)
	}
	return nil
}
