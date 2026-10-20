package runtelemetry

import (
	"context"
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
