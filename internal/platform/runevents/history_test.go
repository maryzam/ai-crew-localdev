package runevents

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindRunRejectsAmbiguousPrefix(t *testing.T) {
	runs := []RunSummary{{RunID: "run_abc111"}, {RunID: "run_abc222"}}
	if _, err := FindRun(runs, "abc"); err == nil {
		t.Fatal("ambiguous run prefix accepted")
	}
}

func TestReadHistoryUsesLatestSnapshotPerRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	started := historyEventLine(t, Event{
		SchemaVersion: SchemaVersion,
		EventType:     "run.started",
		Run:           RunSummary{SchemaVersion: SchemaVersion, RunID: "run_snap", StartedAt: time.Now().UTC()},
	})
	finished := historyEventLine(t, Event{
		SchemaVersion: SchemaVersion,
		EventType:     "run.finished",
		Outcome:       "passed",
		Run: RunSummary{
			SchemaVersion: SchemaVersion, RunID: "run_snap", StartedAt: time.Now().UTC(),
			Outcome: "passed", Broker: BrokerSummary{SessionID: "sess-1"},
		},
	})
	if err := os.WriteFile(path, []byte(started+"\n"+finished+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "run_snap" || runs[0].Outcome != "passed" || runs[0].Broker.SessionID != "sess-1" {
		t.Fatalf("runs = %#v", runs)
	}
}

func TestReadHistoryUsesLiveSnapshotOverRotatedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	rotated := historyEventLine(t, Event{
		SchemaVersion: SchemaVersion,
		EventType:     "run.finished",
		Outcome:       "failed",
		Run: RunSummary{
			SchemaVersion: SchemaVersion,
			RunID:         "run_rotated",
			StartedAt:     time.Now().Add(-time.Minute).UTC(),
			Outcome:       "failed",
		},
	})
	live := historyEventLine(t, Event{
		SchemaVersion: SchemaVersion,
		EventType:     "usage.recorded",
		Run: RunSummary{
			SchemaVersion: SchemaVersion,
			RunID:         "run_rotated",
			StartedAt:     time.Now().Add(-time.Minute).UTC(),
			Outcome:       "passed",
			Broker:        BrokerSummary{SessionID: "sess-live"},
		},
	})
	if err := os.WriteFile(path+".1", []byte(rotated+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(live+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "run_rotated" || runs[0].Outcome != "passed" || runs[0].Broker.SessionID != "sess-live" {
		t.Fatalf("runs = %#v", runs)
	}
}

func TestReadHistoryIgnoresPartialCrashRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	valid := historyEventLine(t, Event{
		SchemaVersion: SchemaVersion,
		EventType:     "run.started",
		Run:           RunSummary{SchemaVersion: SchemaVersion, RunID: "run_valid", StartedAt: time.Now().UTC()},
	})
	data := valid + "\n" + `{"schema_version":`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "run_valid" {
		t.Fatalf("runs = %#v", runs)
	}
}

func TestReadHistoryRejectsUnsupportedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	data := historyEventLine(t, Event{
		SchemaVersion: "3.0",
		EventType:     "run.started",
		Run:           RunSummary{SchemaVersion: "3.0", RunID: "run_future", StartedAt: time.Now().UTC()},
	})
	if err := os.WriteFile(path, []byte(data+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadHistory(path)
	if err == nil {
		t.Fatal("unsupported schema was accepted")
	}
	var unsupported *UnsupportedSchemaError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error = %T %[1]v, want UnsupportedSchemaError", err)
	}
	if unsupported.SchemaVersion != "3.0" || unsupported.SupportedSchemaVersion != SchemaVersion || unsupported.Path != path || unsupported.Line != 1 {
		t.Fatalf("unsupported schema error = %#v", unsupported)
	}
}

func TestReadHistoryUsesUsageSnapshotRecordedAfterFinish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	total := int64(42)
	summary := RunSummary{SchemaVersion: SchemaVersion, RunID: "run_usage", Outcome: "passed"}
	finished := historyEventLine(t, Event{SchemaVersion: SchemaVersion, EventType: "run.finished", Outcome: "passed", Run: summary})
	summary.Usage = &Usage{Status: "estimated", TotalTokens: &total}
	recorded := historyEventLine(t, Event{SchemaVersion: SchemaVersion, EventType: "usage.recorded", Run: summary})
	if err := os.WriteFile(path, []byte(finished+"\n"+recorded+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Usage == nil || runs[0].Usage.TotalTokens == nil || *runs[0].Usage.TotalTokens != total {
		t.Fatalf("runs = %#v", runs)
	}
}

func historyEventLine(t *testing.T, event Event) string {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
