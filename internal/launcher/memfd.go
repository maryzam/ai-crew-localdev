package launcher

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// CreateBindFD creates a sealed memfd containing the bind secret and returns
// the file descriptor number. The memfd is sealed with F_SEAL_SEAL |
// F_SEAL_WRITE | F_SEAL_SHRINK | F_SEAL_GROW to prevent modification by the
// child process tree.
//
// The caller is responsible for passing this FD to the child process as an
// inherited file descriptor. After writing, the offset is reset to 0 so the
// first reader sees the full content.
//
// Helpers and wrappers must reopen via /proc/self/fd/N to obtain a private
// file offset for repeatable reads.
func CreateBindFD(secret []byte) (fd int, err error) {
	fd, err = unix.MemfdCreate("ai-agent-session-bind", unix.MFD_ALLOW_SEALING)
	if err != nil {
		return -1, fmt.Errorf("memfd_create: %w", err)
	}

	// On error, close the fd.
	defer func() {
		if err != nil {
			_ = unix.Close(fd)
		}
	}()

	// Write the bind secret.
	n, err := unix.Write(fd, secret)
	if err != nil {
		return -1, fmt.Errorf("write to memfd: %w", err)
	}
	if n != len(secret) {
		return -1, fmt.Errorf("short write to memfd: %d/%d", n, len(secret))
	}

	// Seek back to start so readers see the content.
	if _, err := unix.Seek(fd, 0, 0); err != nil {
		return -1, fmt.Errorf("seek memfd: %w", err)
	}

	// Seal the memfd to prevent modification.
	seals := unix.F_SEAL_SEAL | unix.F_SEAL_WRITE | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals); err != nil {
		return -1, fmt.Errorf("seal memfd: %w", err)
	}

	return fd, nil
}

// ReadBindSecret reads the bind secret from the FD referenced by
// AI_AGENT_SESSION_BIND_FD. It reopens via /proc/self/fd/N to get an
// independent file offset, reads the content, and closes its copy.
func ReadBindSecret(fd int) ([]byte, error) {
	// Reopen to get independent offset (see architecture doc: FD reopen contract).
	path := fmt.Sprintf("/proc/self/fd/%d", fd)
	newFD, err := unix.Open(path, unix.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("reopen bind FD via %s: %w", path, err)
	}
	defer func() { _ = unix.Close(newFD) }()

	// Read the 32-byte bind secret.
	buf := make([]byte, 64) // slightly oversized to detect corruption
	n, err := unix.Read(newFD, buf)
	if err != nil {
		return nil, fmt.Errorf("read bind secret: %w", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("bind secret FD is empty")
	}

	return buf[:n], nil
}
