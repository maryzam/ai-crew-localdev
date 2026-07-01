package launcher

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func CreateBindFD(secret []byte) (fd int, err error) {
	fd, err = unix.MemfdCreate("ai-agent-session-bind", unix.MFD_ALLOW_SEALING)
	if err != nil {
		return -1, fmt.Errorf("memfd_create: %w", err)
	}

	defer func() {
		if err != nil {
			_ = unix.Close(fd)
		}
	}()

	n, err := unix.Write(fd, secret)
	if err != nil {
		return -1, fmt.Errorf("write to memfd: %w", err)
	}
	if n != len(secret) {
		return -1, fmt.Errorf("short write to memfd: %d/%d", n, len(secret))
	}

	if _, err := unix.Seek(fd, 0, 0); err != nil {
		return -1, fmt.Errorf("seek memfd: %w", err)
	}

	seals := unix.F_SEAL_SEAL | unix.F_SEAL_WRITE | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals); err != nil {
		return -1, fmt.Errorf("seal memfd: %w", err)
	}

	return fd, nil
}
