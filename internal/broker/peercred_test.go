package broker

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPeerCred(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	errCh := make(chan error, 1)
	uidCh := make(chan uint32, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()

		uid, _, _, err := PeerCred(conn.(*net.UnixConn))
		if err != nil {
			errCh <- err
			return
		}
		uidCh <- uid
	}()

	client, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	select {
	case err := <-errCh:
		t.Fatalf("PeerCred: %v", err)
	case uid := <-uidCh:
		expectedUID := uint32(os.Getuid())
		if uid != expectedUID {
			t.Errorf("PeerCred UID = %d, want %d", uid, expectedUID)
		}
	}
}
