package launcher

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestCreateBindFDIsSealed(t *testing.T) {
	secret := []byte("test-secret-32-bytes-for-memfd!!")

	fd, err := CreateBindFD(secret)
	if err != nil {
		t.Fatalf("CreateBindFD: %v", err)
	}
	defer func() { _ = unix.Close(fd) }()

	_, err = unix.Write(fd, []byte("overwrite"))
	if err == nil {
		t.Error("expected write to sealed memfd to fail")
	}
}
