package sessionauth_test

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/launcher"
	"github.com/maryzam/ai-crew-localdev/internal/sessionauth"
	"golang.org/x/sys/unix"
)

func TestLoadFromLauncherBindFD(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	fd, err := launcher.CreateBindFD(secret)
	if err != nil {
		t.Fatalf("CreateBindFD: %v", err)
	}
	defer func() { _ = unix.Close(fd) }()

	t.Setenv("AI_AGENT_AUTH_SOCK", "/run/ai-agent/broker.sock")
	t.Setenv("AI_AGENT_SESSION_ID", "sess-123")
	t.Setenv("AI_AGENT_SESSION_BIND_FD", strconv.Itoa(fd))

	session, err := sessionauth.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(session.BindSecret, secret) {
		t.Fatalf("BindSecret = %q, want %q", session.BindSecret, secret)
	}
}
