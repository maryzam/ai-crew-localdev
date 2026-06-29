package securefile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
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

func TestWriteOwnerOnlyFailureBoundaries(t *testing.T) {
	injected := errors.New("injected failure")
	tests := []struct {
		name    string
		mutate  func(*writeOperations)
		wantNew bool
	}{
		{name: "create", mutate: func(operations *writeOperations) {
			operations.create = func(string, string) (*os.File, error) { return nil, injected }
		}},
		{name: "chmod", mutate: func(operations *writeOperations) {
			operations.chmod = func(*os.File, os.FileMode) error { return injected }
		}},
		{name: "write", mutate: func(operations *writeOperations) { operations.write = func(*os.File, []byte) error { return injected } }},
		{name: "file sync", mutate: func(operations *writeOperations) { operations.syncFile = func(*os.File) error { return injected } }},
		{name: "close", mutate: func(operations *writeOperations) {
			operations.close = func(file *os.File) error {
				_ = file.Close()
				return injected
			}
		}},
		{name: "rename", mutate: func(operations *writeOperations) { operations.rename = func(string, string) error { return injected } }},
		{name: "directory sync", wantNew: true, mutate: func(operations *writeOperations) { operations.syncDirectory = func(string) error { return injected } }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state")
			if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			operations := defaultWriteOperations()
			test.mutate(&operations)
			if err := writeOwnerOnly(path, []byte("new"), operations); !errors.Is(err, injected) {
				t.Fatalf("error = %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := "old"
			if test.wantNew {
				want = "new"
			}
			if string(data) != want {
				t.Fatalf("data = %q, want %q", data, want)
			}
			debris, err := filepath.Glob(filepath.Join(dir, ".state.tmp-*"))
			if err != nil {
				t.Fatal(err)
			}
			if len(debris) != 0 {
				t.Fatalf("temporary files remain: %v", debris)
			}
		})
	}
}
