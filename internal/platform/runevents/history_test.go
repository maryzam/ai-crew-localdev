package runevents

import (
	"encoding/json"
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
