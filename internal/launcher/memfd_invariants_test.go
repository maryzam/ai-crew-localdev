package launcher

import (
	"bytes"
	"testing"

	"golang.org/x/sys/unix"
)

// Invariant: a sealed memfd must reject writes. This ensures the bind secret
// cannot be modified by the child process tree after delivery.
func TestInvariant_MemfdIsSealed(t *testing.T) {
	secret := []byte("invariant-test-32-bytes-secret!!")

	fd, err := CreateBindFD(secret)
	if err != nil {
		t.Fatalf("CreateBindFD: %v", err)
	}
	defer unix.Close(fd)

	// Write after seal must return EPERM (or any error).
	_, err = unix.Write(fd, []byte("tamper"))
	if err == nil {
		t.Fatal("write to sealed memfd succeeded; expected EPERM")
	}
	if err != unix.EPERM {
		t.Logf("write returned %v (expected EPERM but any error is acceptable)", err)
	}
}

// Invariant: ReadBindSecret must return the exact bytes written by
// CreateBindFD. This proves the round-trip through memfd + /proc/self/fd
// reopen preserves the secret without corruption.
func TestInvariant_MemfdRoundTrip(t *testing.T) {
	secret := []byte("roundtrip-test-32-bytes-secret!!")

	fd, err := CreateBindFD(secret)
	if err != nil {
		t.Fatalf("CreateBindFD: %v", err)
	}
	defer unix.Close(fd)

	got, err := ReadBindSecret(fd)
	if err != nil {
		t.Fatalf("ReadBindSecret: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Errorf("round-trip mismatch: got %q, want %q", got, secret)
	}

	// Second read must also succeed (independent offset via /proc/self/fd).
	got2, err := ReadBindSecret(fd)
	if err != nil {
		t.Fatalf("ReadBindSecret (second): %v", err)
	}
	if !bytes.Equal(got2, secret) {
		t.Errorf("second round-trip mismatch: got %q, want %q", got2, secret)
	}
}
