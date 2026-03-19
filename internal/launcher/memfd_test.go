package launcher

import (
	"bytes"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCreateBindFDAndRead(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef") // 32 bytes

	fd, err := CreateBindFD(secret)
	if err != nil {
		t.Fatalf("CreateBindFD: %v", err)
	}
	defer func() { _ = unix.Close(fd) }()

	// Verify we can read the secret back.
	got, err := ReadBindSecret(fd)
	if err != nil {
		t.Fatalf("ReadBindSecret: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Errorf("read %d bytes, want %d", len(got), len(secret))
	}

	// Verify repeatable reads (different callers get independent offsets).
	got2, err := ReadBindSecret(fd)
	if err != nil {
		t.Fatalf("ReadBindSecret (second): %v", err)
	}
	if !bytes.Equal(got2, secret) {
		t.Errorf("second read got %d bytes, want %d", len(got2), len(secret))
	}
}

func TestCreateBindFDIsSealed(t *testing.T) {
	secret := []byte("test-secret-32-bytes-for-memfd!!")

	fd, err := CreateBindFD(secret)
	if err != nil {
		t.Fatalf("CreateBindFD: %v", err)
	}
	defer func() { _ = unix.Close(fd) }()

	// Attempt to write to the sealed memfd should fail.
	_, err = unix.Write(fd, []byte("overwrite"))
	if err == nil {
		t.Error("expected write to sealed memfd to fail")
	}
}
