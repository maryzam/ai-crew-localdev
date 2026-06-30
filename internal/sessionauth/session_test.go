package sessionauth

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLoad(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	fd := createSealedBindFD(t, secret)
	t.Setenv("AI_AGENT_AUTH_SOCK", "/run/ai-agent/broker.sock")
	t.Setenv("AI_AGENT_SESSION_ID", "sess-123")
	t.Setenv("AI_AGENT_SESSION_BIND_FD", strconv.Itoa(fd))

	first, err := Load()
	if err != nil {
		t.Fatalf("Load first call: %v", err)
	}
	second, err := Load()
	if err != nil {
		t.Fatalf("Load second call: %v", err)
	}

	if first.SocketPath != "/run/ai-agent/broker.sock" {
		t.Fatalf("SocketPath = %q", first.SocketPath)
	}
	if first.SessionID != "sess-123" {
		t.Fatalf("SessionID = %q", first.SessionID)
	}
	if !bytes.Equal(first.BindSecret, secret) {
		t.Fatalf("first BindSecret = %q, want %q", first.BindSecret, secret)
	}
	if !bytes.Equal(second.BindSecret, secret) {
		t.Fatalf("second BindSecret = %q, want %q", second.BindSecret, secret)
	}
}

func TestLoadRequiresManagedSessionEnvironment(t *testing.T) {
	t.Setenv("AI_AGENT_AUTH_SOCK", "")
	t.Setenv("AI_AGENT_SESSION_ID", "")
	t.Setenv("AI_AGENT_SESSION_BIND_FD", "")

	tests := []struct {
		name       string
		socketPath string
		sessionID  string
		bindFD     string
		want       string
	}{
		{name: "socket", want: "AI_AGENT_AUTH_SOCK not set"},
		{name: "session", socketPath: "/run/broker.sock", want: "AI_AGENT_SESSION_ID not set"},
		{name: "bind fd", socketPath: "/run/broker.sock", sessionID: "sess-123", want: "AI_AGENT_SESSION_BIND_FD not set"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AI_AGENT_AUTH_SOCK", test.socketPath)
			t.Setenv("AI_AGENT_SESSION_ID", test.sessionID)
			t.Setenv("AI_AGENT_SESSION_BIND_FD", test.bindFD)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsInvalidBindFD(t *testing.T) {
	t.Setenv("AI_AGENT_AUTH_SOCK", "/run/broker.sock")
	t.Setenv("AI_AGENT_SESSION_ID", "sess-123")
	t.Setenv("AI_AGENT_SESSION_BIND_FD", "invalid")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "invalid AI_AGENT_SESSION_BIND_FD") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsEmptyBindSecret(t *testing.T) {
	fd := createSealedBindFD(t, nil)
	t.Setenv("AI_AGENT_AUTH_SOCK", "/run/broker.sock")
	t.Setenv("AI_AGENT_SESSION_ID", "sess-123")
	t.Setenv("AI_AGENT_SESSION_BIND_FD", strconv.Itoa(fd))

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "bind secret must be 32 bytes, got 0") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsOversizedBindSecret(t *testing.T) {
	fd := createSealedBindFD(t, make([]byte, 33))
	t.Setenv("AI_AGENT_AUTH_SOCK", "/run/broker.sock")
	t.Setenv("AI_AGENT_SESSION_ID", "sess-123")
	t.Setenv("AI_AGENT_SESSION_BIND_FD", strconv.Itoa(fd))

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "bind secret must be 32 bytes, got 33") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsUndersizedBindSecret(t *testing.T) {
	fd := createSealedBindFD(t, make([]byte, 31))
	t.Setenv("AI_AGENT_AUTH_SOCK", "/run/broker.sock")
	t.Setenv("AI_AGENT_SESSION_ID", "sess-123")
	t.Setenv("AI_AGENT_SESSION_BIND_FD", strconv.Itoa(fd))

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "bind secret must be 32 bytes, got 31") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsUnsealedFile(t *testing.T) {
	file := createSealedBindFD(t, make([]byte, 32))
	if _, err := unix.FcntlInt(uintptr(file), unix.F_ADD_SEALS, unix.F_SEAL_SEAL); err == nil {
		t.Fatal("expected sealed descriptor to reject seal changes")
	}

	regular, err := unix.Open("/dev/null", unix.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(regular) })
	t.Setenv("AI_AGENT_AUTH_SOCK", "/run/broker.sock")
	t.Setenv("AI_AGENT_SESSION_ID", "sess-123")
	t.Setenv("AI_AGENT_SESSION_BIND_FD", strconv.Itoa(regular))

	_, err = Load()
	if err == nil || !strings.Contains(err.Error(), "not a sealable memfd") {
		t.Fatalf("Load error = %v", err)
	}
}

func createSealedBindFD(t *testing.T, secret []byte) int {
	t.Helper()
	fd, err := unix.MemfdCreate("ai-agent-bind", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		t.Fatalf("MemfdCreate: %v", err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	if _, err := unix.Write(fd, secret); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, requiredSeals); err != nil {
		t.Fatalf("F_ADD_SEALS: %v", err)
	}
	return fd
}
