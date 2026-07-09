package brokerd

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateActivatedSocketRejectsMismatchedPaths(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "broker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	if err := validateActivatedSocket(listener.Addr(), socketPath); err != nil {
		t.Fatalf("matching activated socket rejected: %v", err)
	}

	err = validateActivatedSocket(listener.Addr(), filepath.Join(t.TempDir(), "custom.sock"))
	if err == nil || !strings.Contains(err.Error(), "does not match the configured broker socket") {
		t.Fatalf("mismatched activated socket accepted: %v", err)
	}
}
