package securefile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

var ErrBusy = errors.New("directory is locked by another process")

const lockRetryInterval = 10 * time.Millisecond

func WriteOwnerOnly(path string, data []byte) error {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporary := file.Name()
	defer func() { _ = file.Close() }()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("publish file: %w", err)
	}
	if err := SyncDirectory(directory); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}

func openOwnerOnly(path string) (*os.File, unix.Stat_t, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, unix.Stat_t{}, fmt.Errorf("open secure file: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, unix.Stat_t{}, fmt.Errorf("open secure file")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return nil, unix.Stat_t{}, fmt.Errorf("inspect secure file: %w", err)
	}
	if err := requireOwnerOnly(stat, unix.S_IFREG); err != nil {
		_ = file.Close()
		return nil, unix.Stat_t{}, fmt.Errorf("secure file %w", err)
	}
	return file, stat, nil
}

func requireOwnerOnly(stat unix.Stat_t, kind uint32) error {
	if stat.Mode&unix.S_IFMT != kind {
		if kind == unix.S_IFDIR {
			return errors.New("must be a directory, not a link or file")
		}
		return errors.New("must be regular")
	}
	if stat.Uid != uint32(os.Getuid()) {
		return errors.New("owner does not match current user")
	}
	if stat.Mode&0o077 != 0 {
		return errors.New("must be owner-only")
	}
	return nil
}

func ReadOwnerOnly(path string, maxBytes int64) ([]byte, error) {
	file, stat, err := openOwnerOnly(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if maxBytes <= 0 || stat.Size > maxBytes {
		return nil, fmt.Errorf("secure file exceeds %d bytes", maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read secure file: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("secure file exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func HashOwnerOnly(path string, maxBytes int64) (string, error) {
	file, stat, err := openOwnerOnly(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	if maxBytes <= 0 || stat.Size > maxBytes {
		return "", fmt.Errorf("secure file exceeds %d bytes", maxBytes)
	}
	digest := sha256.New()
	copied, err := io.Copy(digest, io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read secure file: %w", err)
	}
	if copied > maxBytes {
		return "", fmt.Errorf("secure file exceeds %d bytes", maxBytes)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func EnsureOwnerOnlyDir(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("private directory %q must be absolute", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create private directory parent: %w", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create private directory: %w", err)
	}
	return RequireOwnerOnlyDir(path)
}

func RequireOwnerOnlyDir(path string) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return fmt.Errorf("inspect private directory %s: %w", path, err)
	}
	if err := requireOwnerOnly(stat, unix.S_IFDIR); err != nil {
		return fmt.Errorf("private directory %s %w", path, err)
	}
	return nil
}

func LockDirectory(path string, budget time.Duration) (*os.File, error) {
	directory, err := os.OpenFile(path, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open lock directory %s: %w", path, err)
	}
	deadline := time.Now().Add(budget)
	for {
		err := unix.Flock(int(directory.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return directory, nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = directory.Close()
			return nil, fmt.Errorf("lock directory %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			_ = directory.Close()
			return nil, fmt.Errorf("%w: %s after %s", ErrBusy, path, budget)
		}
		time.Sleep(lockRetryInterval)
	}
}

func SyncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
