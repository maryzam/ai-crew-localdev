package main

import (
	"bytes"
	"os"
	"strconv"
	"testing"
)

func TestLoadManagedSession_FileBackedFD(t *testing.T) {
	secret := []byte("fedcba9876543210fedcba9876543210")
	fd := createUnlinkedBindFile(t, secret)

	env := map[string]string{
		"AI_AGENT_AUTH_SOCK":       "/run/ai-agent/broker.sock",
		"AI_AGENT_SESSION_ID":      "sess-456",
		"AI_AGENT_SESSION_BIND_FD": strconv.Itoa(fd),
	}

	getenv := func(key string) string { return env[key] }

	first, err := loadManagedSession(getenv)
	if err != nil {
		t.Fatalf("loadManagedSession(first): %v", err)
	}
	second, err := loadManagedSession(getenv)
	if err != nil {
		t.Fatalf("loadManagedSession(second): %v", err)
	}

	if first.SocketPath != env["AI_AGENT_AUTH_SOCK"] {
		t.Fatalf("SocketPath = %q, want %q", first.SocketPath, env["AI_AGENT_AUTH_SOCK"])
	}
	if first.SessionID != env["AI_AGENT_SESSION_ID"] {
		t.Fatalf("SessionID = %q, want %q", first.SessionID, env["AI_AGENT_SESSION_ID"])
	}
	if !bytes.Equal(first.BindSecret, secret) {
		t.Fatalf("first BindSecret = %q, want %q", first.BindSecret, secret)
	}
	if !bytes.Equal(second.BindSecret, secret) {
		t.Fatalf("second BindSecret = %q, want %q", second.BindSecret, secret)
	}
}

func createUnlinkedBindFile(t *testing.T, secret []byte) int {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "bind-secret-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	if _, err := f.Write(secret); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if err := os.Remove(f.Name()); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	return int(f.Fd())
}
