package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadRunHistoryDelegatesToRunEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	line := writeEventLine(t, Event{
		SchemaVersion: SchemaVersion,
		EventType:     "run.started",
		Run:           RunSummary{SchemaVersion: SchemaVersion, RunID: "run_delegate", StartedAt: time.Now().UTC()},
	})
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, err := ReadRunHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "run_delegate" {
		t.Fatalf("runs = %#v", runs)
	}
}

func TestFindRunDelegatesToRunEvents(t *testing.T) {
	run, err := FindRun([]RunSummary{{RunID: "run_abc123"}}, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID != "run_abc123" {
		t.Fatalf("run = %#v", run)
	}
}

func writeEventLine(t *testing.T, event Event) string {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
