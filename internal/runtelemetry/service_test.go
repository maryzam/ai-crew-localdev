package runtelemetry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

func TestServiceCloseWaitsForAcquiredRecordAndRejectsLateRecords(t *testing.T) {
	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(root, nil)
	runContext := Context{RunID: "run_close_race", Task: "task", Project: "project", Agents: map[Agent]string{Codex: "1.0"}}
	entry, err := service.acquire(runContext)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- service.Close(runContext.RunID) }()
	deadline, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		service.mu.Lock()
		closing := entry.closing
		service.mu.Unlock()
		if closing {
			break
		}
		select {
		case <-deadline.Done():
			t.Fatal("service did not begin closing")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case err := <-closed:
		t.Fatalf("close completed with an acquired record: %v", err)
	default:
	}
	payload := []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{}]}]}]}`)
	if err := entry.receiver.Store(Codex, "traces", payload); err != nil {
		t.Fatal(err)
	}
	entry.inflight.Done()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if stats := entry.receiver.Stats(); stats.Stored != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if err := service.Record(runContext, Codex, "traces", payload); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("late record error = %v", err)
	}
}

func TestServiceBoundsEachRunAndPrunesInactiveRunsWhenARunStarts(t *testing.T) {
	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	old := sparseTelemetry(t, root, "run_old", evidence.DefaultTelemetryBudget+1, time.Now().Add(-time.Hour))
	fullPath := sparseTelemetry(t, root, "run_full", evidence.MaxRunTelemetryBytes-4, time.Now())
	service := NewService(root, nil)
	payload := []byte(`{"resourceSpans":[]}`)
	runContext := func(run string) Context {
		return Context{RunID: run, Task: "task", Project: "project", Agents: map[Agent]string{Codex: "1.0"}}
	}
	if err := service.Record(runContext("run_new"), Codex, "traces", payload); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest inactive run was not pruned: %v", err)
	}
	if err := service.Record(runContext("run_full"), Codex, "traces", payload); !errors.Is(err, evidence.ErrRunTelemetryQuota) {
		t.Fatalf("over-quota record error = %v", err)
	}
	if err := service.CloseAll(); err != nil {
		t.Fatal(err)
	}
	pruned, err := Summarize(root, "run_old")
	if err != nil || len(pruned.Pruned) != 1 {
		t.Fatalf("pruning evidence = %+v, %v", pruned.Pruned, err)
	}
	if info, err := os.Stat(fullPath); err != nil || info.Size() != evidence.MaxRunTelemetryBytes-4 {
		t.Fatalf("over-quota run changed: %v", err)
	}
	if err := os.Truncate(fullPath, 0); err != nil {
		t.Fatal(err)
	}
	full, err := Summarize(root, "run_full")
	if err != nil || full.Receiver == nil || full.Receiver.Rejected != 1 || full.Receiver.Stored != 0 {
		t.Fatalf("over-quota summary = %+v, %v", full, err)
	}
}

func sparseTelemetry(t *testing.T, root *evidence.Root, run string, size int64, modified time.Time) string {
	t.Helper()
	dir, err := root.RunDir(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, evidence.TelemetryFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	return path
}
