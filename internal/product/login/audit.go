package login

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type auditEvent struct {
	Time       time.Time `json:"time"`
	Agent      string    `json:"agent"`
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason,omitempty"`
	Image      string    `json:"image,omitempty"`
	BinaryHash string    `json:"binary_sha256,omitempty"`
	DurationMS int64     `json:"duration_ms"`
}

type auditLog struct {
	file   *os.File
	failed error
}

func openAudit(path string) (*auditLog, error) {
	fd, err := unix.Open(path, unix.O_APPEND|unix.O_CREAT|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open login audit: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open login audit")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		_ = file.Close()
		return nil, errors.New("login audit must be an owner-only regular file")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		_ = file.Close()
		return nil, errors.New("login audit must be owned by the current user")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errors.New("another login is already in progress")
	}
	return &auditLog{file: file}, nil
}

func (a *auditLog) record(event auditEvent) error {
	if a.failed != nil {
		return a.failed
	}
	line, err := json.Marshal(event)
	if err == nil {
		line = append(line, '\n')
		var written int
		written, err = a.file.Write(line)
		if err == nil && written != len(line) {
			err = errors.New("short audit write")
		}
	}
	if err == nil {
		err = a.file.Sync()
	}
	if err != nil {
		a.failed = fmt.Errorf("login audit unavailable: %w", err)
		return a.failed
	}
	return nil
}

func (a *auditLog) close() error {
	if a.file == nil {
		return nil
	}
	return a.file.Close()
}
