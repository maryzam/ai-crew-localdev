package securefile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWriteOwnerOnlyPublishesCompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteOwnerOnly(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, err := ReadOwnerOnly(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("data = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestWriteOwnerOnlyReplacesSymlinkWithoutFollowingIt(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, "state")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := WriteOwnerOnly(path, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	targetData, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(targetData) != "target" {
		t.Fatalf("target data = %q", targetData)
	}
	data, err := ReadOwnerOnly(path, 32)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "replacement" {
		t.Fatalf("replacement data = %q", data)
	}
}

func TestReadOwnerOnlyRejectsUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	insecure := filepath.Join(dir, "insecure")
	if err := os.WriteFile(insecure, []byte("value"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnerOnly(insecure, 16); err == nil {
		t.Fatal("insecure mode accepted")
	}
	symlink := filepath.Join(dir, "symlink")
	if err := os.Symlink(insecure, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnerOnly(symlink, 16); err == nil {
		t.Fatal("symlink accepted")
	}
	large := filepath.Join(dir, "large")
	if err := os.WriteFile(large, bytes.Repeat([]byte("x"), 17), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnerOnly(large, 16); err == nil {
		t.Fatal("oversized file accepted")
	}
}

func TestWriteOwnerOnlyConcurrentReadersSeeCompleteValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	oldValue := bytes.Repeat([]byte("a"), 4096)
	newValue := bytes.Repeat([]byte("b"), 4096)
	if err := WriteOwnerOnly(path, oldValue); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	wait.Add(1)
	go func() {
		defer wait.Done()
		errs <- WriteOwnerOnly(path, newValue)
	}()
	wait.Add(1)
	go func() {
		defer wait.Done()
		for range 32 {
			value, err := ReadOwnerOnly(path, 8192)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(value, oldValue) && !bytes.Equal(value, newValue) {
				errs <- bytes.ErrTooLarge
				return
			}
		}
		errs <- nil
	}()
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnsureOwnerOnlyDirRejectsSharedOrLinkedDirectories(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "a", "b")
	if err := EnsureOwnerOnlyDir(private); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := EnsureOwnerOnlyDir(shared); err == nil {
		t.Fatal("group-accessible directory accepted")
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(private, linked); err != nil {
		t.Fatal(err)
	}
	if err := EnsureOwnerOnlyDir(linked); err == nil {
		t.Fatal("symlinked directory accepted")
	}
	if err := EnsureOwnerOnlyDir("relative"); err == nil {
		t.Fatal("relative directory accepted")
	}
}

func TestLockDirectoryAdmitsOneHolderWithinBudget(t *testing.T) {
	dir := t.TempDir()
	held, err := LockDirectory(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockDirectory(dir, 20*time.Millisecond); !errors.Is(err, ErrBusy) {
		t.Fatalf("second lock error = %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := LockDirectory(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
}

func TestHashOwnerOnlyStreamsBoundedPrivateFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "program")
	if err := WriteOwnerOnly(path, []byte("program")); err != nil {
		t.Fatal(err)
	}
	hash, err := HashOwnerOnly(path, 16)
	if err != nil || hash != "1310ca2c8932dbd118668bd97442558a3e1f546b5e15c69699dfbd6024f548d8" {
		t.Fatalf("hash = %q, %v", hash, err)
	}
	if _, err := HashOwnerOnly(path, 4); err == nil {
		t.Fatal("oversized file hashed")
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := HashOwnerOnly(path, 16); err == nil {
		t.Fatal("shared file hashed")
	}
}
