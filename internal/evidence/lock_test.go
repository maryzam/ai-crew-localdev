package evidence

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectoryLockFailsWithinBudgetAndRecovers(t *testing.T) {
	root, err := OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := root.RunDir("run-lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := lockDirectory(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockDirectory(dir, 20*time.Millisecond); !errors.Is(err, ErrEvidenceBusy) {
		t.Fatalf("contended lock returned %v, want ErrEvidenceBusy", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := lockDirectory(dir, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("released lock could not be reacquired: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}
