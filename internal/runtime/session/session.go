package session

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

const bindSecretBytes = 32

const requiredSeals = unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE

type Session struct {
	SocketPath string
	SessionID  string
	BindSecret []byte
}

func Load() (Session, error) {
	socketPath := os.Getenv("AI_AGENT_AUTH_SOCK")
	if socketPath == "" {
		return Session{}, fmt.Errorf("AI_AGENT_AUTH_SOCK not set; not in a managed session")
	}

	sessionID := os.Getenv("AI_AGENT_SESSION_ID")
	if sessionID == "" {
		return Session{}, fmt.Errorf("AI_AGENT_SESSION_ID not set; not in a managed session")
	}

	bindFDValue := os.Getenv("AI_AGENT_SESSION_BIND_FD")
	if bindFDValue == "" {
		return Session{}, fmt.Errorf("AI_AGENT_SESSION_BIND_FD not set; not in a managed session")
	}
	bindFD, err := strconv.Atoi(bindFDValue)
	if err != nil {
		return Session{}, fmt.Errorf("invalid AI_AGENT_SESSION_BIND_FD: %w", err)
	}

	bindSecret, err := readBindSecret(bindFD)
	if err != nil {
		return Session{}, fmt.Errorf("read bind secret: %w", err)
	}

	return Session{SocketPath: socketPath, SessionID: sessionID, BindSecret: bindSecret}, nil
}

func readBindSecret(fd int) ([]byte, error) {
	path := fmt.Sprintf("/proc/self/fd/%d", fd)
	copyFD, err := unix.Open(path, unix.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("reopen bind FD via %s: %w", path, err)
	}
	defer func() { _ = unix.Close(copyFD) }()
	seals, err := unix.FcntlInt(uintptr(copyFD), unix.F_GET_SEALS, 0)
	if err != nil {
		return nil, fmt.Errorf("bind secret FD is not a sealable memfd: %w", err)
	}
	if seals&requiredSeals != requiredSeals {
		return nil, fmt.Errorf("bind secret FD is missing required seals")
	}

	secret := make([]byte, bindSecretBytes+1)
	n, err := unix.Read(copyFD, secret)
	if err != nil {
		return nil, fmt.Errorf("read bind secret: %w", err)
	}
	if n != bindSecretBytes {
		return nil, fmt.Errorf("bind secret must be %d bytes, got %d", bindSecretBytes, n)
	}
	return secret[:n], nil
}
