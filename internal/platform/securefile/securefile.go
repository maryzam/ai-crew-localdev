package securefile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

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
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = file.Close()
		return nil, unix.Stat_t{}, fmt.Errorf("secure file must be regular")
	}
	if stat.Uid != uint32(os.Getuid()) {
		_ = file.Close()
		return nil, unix.Stat_t{}, fmt.Errorf("secure file owner does not match current user")
	}
	if stat.Mode&0o077 != 0 {
		_ = file.Close()
		return nil, unix.Stat_t{}, fmt.Errorf("secure file must be owner-only")
	}
	return file, stat, nil
}

func StatOwnerOnly(path string) (os.FileInfo, error) {
	file, _, err := openOwnerOnly(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect secure file: %w", err)
	}
	return info, nil
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

func Remove(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	return SyncDirectory(filepath.Dir(path))
}

func SyncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
