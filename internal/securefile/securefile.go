package securefile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type writeOperations struct {
	create        func(string, string) (*os.File, error)
	chmod         func(*os.File, os.FileMode) error
	write         func(*os.File, []byte) error
	syncFile      func(*os.File) error
	close         func(*os.File) error
	rename        func(string, string) error
	remove        func(string) error
	syncDirectory func(string) error
}

func defaultWriteOperations() writeOperations {
	return writeOperations{
		create:        os.CreateTemp,
		chmod:         func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) },
		write:         writeAll,
		syncFile:      func(file *os.File) error { return file.Sync() },
		close:         func(file *os.File) error { return file.Close() },
		rename:        os.Rename,
		remove:        os.Remove,
		syncDirectory: SyncDirectory,
	}
}

func WriteOwnerOnly(path string, data []byte) error {
	return writeOwnerOnly(path, data, defaultWriteOperations())
}

func writeOwnerOnly(path string, data []byte, operations writeOperations) error {
	directory := filepath.Dir(path)
	file, err := operations.create(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporary := file.Name()
	keep := false
	defer func() {
		if !keep {
			_ = operations.remove(temporary)
		}
	}()
	if err := operations.chmod(file, 0o600); err != nil {
		_ = operations.close(file)
		return fmt.Errorf("secure temporary file: %w", err)
	}
	if err := operations.write(file, data); err != nil {
		_ = operations.close(file)
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := operations.syncFile(file); err != nil {
		_ = operations.close(file)
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := operations.close(file); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := operations.rename(temporary, path); err != nil {
		return fmt.Errorf("publish file: %w", err)
	}
	keep = true
	if err := operations.syncDirectory(directory); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}

func ReadOwnerOnly(path string, maxBytes int64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open secure file: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open secure file")
	}
	defer func() { _ = file.Close() }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, fmt.Errorf("inspect secure file: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, fmt.Errorf("secure file must be regular")
	}
	if stat.Uid != uint32(os.Getuid()) {
		return nil, fmt.Errorf("secure file owner does not match current user")
	}
	if stat.Mode&0o077 != 0 {
		return nil, fmt.Errorf("secure file must be owner-only")
	}
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

func writeAll(file *os.File, data []byte) error {
	for len(data) > 0 {
		written, err := file.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func SyncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
