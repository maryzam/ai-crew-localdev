package session

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

const bindSecretBytes = 32

const requiredSeals = unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE

type Session struct {
	SocketPath string
	SessionID  string
	BindSecret []byte
}

func Load() (Session, error) {
	socketPath := os.Getenv(paths.EnvAuthSock)
	if socketPath == "" {
		return Session{}, fmt.Errorf("%s not set; not in a managed session", paths.EnvAuthSock)
	}

	sessionID := os.Getenv(paths.EnvSessionID)
	if sessionID == "" {
		return Session{}, fmt.Errorf("%s not set; not in a managed session", paths.EnvSessionID)
	}

	bindFDValue := os.Getenv(paths.EnvSessionBindFD)
	if bindFDValue == "" {
		return Session{}, fmt.Errorf("%s not set; not in a managed session", paths.EnvSessionBindFD)
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
