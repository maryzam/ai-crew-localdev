package runevents

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStoreRotatesExistingLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run-events.jsonl")
	if err := os.WriteFile(logPath, []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store, err := OpenStoreSized(logPath, 8)
	if err != nil {
		t.Fatalf("OpenStoreSized: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	backup, err := os.ReadFile(logPath + ".1")
	if err != nil || string(backup) != "0123456789" {
		t.Fatalf("rotated backup = %q, err=%v", backup, err)
	}
	info, err := os.Stat(logPath + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("rotated backup mode = %o, want 600", got)
	}
}

func TestStoreSerializesConcurrentWritersAndSecuresPermissions(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run-events.jsonl")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	const writers = 32
	stores := make([]*Store, writers)
	for index := range stores {
		store, err := OpenStoreSized(logPath, 1024*1024)
		if err != nil {
			t.Fatal(err)
		}
		stores[index] = store
	}

	var group sync.WaitGroup
	for index, store := range stores {
		group.Add(1)
		go func() {
			defer group.Done()
			event := storeEvent(fmt.Sprintf("run_%032x", index))
			if err := store.Write(event); err != nil {
				t.Errorf("write event: %v", err)
			}
		}()
	}
	group.Wait()
	for _, store := range stores {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := ReadHistory(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != writers {
		t.Fatalf("runs = %d, want %d", len(runs), writers)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("run events mode = %o, want 600", got)
	}
}

func TestStoreRejectsSymbolicLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("do not append"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "runs.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(filepath.Join(dir, "runs.jsonl")); err == nil {
		t.Fatal("symbolic-link run event path accepted")
	}
}

func TestStoreRejectsWriteAfterClose(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "run-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(storeEvent("run_closed")); err == nil {
		t.Fatal("write after close accepted")
	}
}

func storeEvent(runID string) Event {
	return Event{
		SchemaVersion: SchemaVersion,
		Timestamp:     time.Now().UTC(),
		EventType:     "run.started",
		Run: RunSummary{
			SchemaVersion: SchemaVersion,
			RunID:         runID,
			StartedAt:     time.Now().UTC(),
		},
	}
}
