package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
	"golang.org/x/sys/unix"
)

const metadataMaxBytes = 64 << 10
const sourceLockTimeout = 10 * time.Second
const runLockTimeout = 10 * time.Second

type activeWorkspace struct {
	ID string `json:"id"`
}

func ensureOwnerDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0o077 != 0 || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("workspace directory %s must be an owner-only directory", path)
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data)+1 > metadataMaxBytes {
		return fmt.Errorf("workspace metadata exceeds %d bytes", metadataMaxBytes)
	}
	return securefile.WriteOwnerOnly(path, append(data, '\n'))
}

func readJSON(path string, value any) error {
	data, err := securefile.ReadOwnerOnly(path, metadataMaxBytes)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func withSourceLock(ctx context.Context, directory string, action func() error) error {
	lockCtx, cancel := context.WithTimeout(ctx, sourceLockTimeout)
	defer cancel()
	if err := ensureOwnerDirectory(directory); err != nil {
		return err
	}
	lockPath := filepath.Join(directory, "workspace.lock")
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open workspace lock: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect workspace lock: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("workspace lock must be an owner-only regular file")
	}
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return fmt.Errorf("lock workspace: %w", err)
		}
		select {
		case <-lockCtx.Done():
			return fmt.Errorf("lock workspace within %s: %w", sourceLockTimeout, lockCtx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer func() { _ = unix.Flock(fd, unix.LOCK_UN) }()
	return action()
}

func acquireRunLock(ctx context.Context, path string) (int, error) {
	lockCtx, cancel := context.WithTimeout(ctx, runLockTimeout)
	defer cancel()
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return -1, fmt.Errorf("open workspace run lock: %w", err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("inspect workspace run lock: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Getuid()) {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("workspace run lock must be an owner-only regular file")
	}
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return fd, nil
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			_ = unix.Close(fd)
			return -1, fmt.Errorf("lock running workspace: %w", err)
		}
		select {
		case <-lockCtx.Done():
			_ = unix.Close(fd)
			return -1, fmt.Errorf("lock running workspace within %s: %w", runLockTimeout, lockCtx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func releaseRunLock(fd int) error {
	if fd < 0 {
		return nil
	}
	unlockErr := unix.Flock(fd, unix.LOCK_UN)
	closeErr := unix.Close(fd)
	if unlockErr != nil {
		return fmt.Errorf("unlock running workspace: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close workspace run lock: %w", closeErr)
	}
	return nil
}
