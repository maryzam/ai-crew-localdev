package telemetry

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

func TestReadRunHistoryUsesLatestSnapshotPerRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	started := writeEventLine(t, Event{
		SchemaVersion: SchemaVersion,
		EventType:     "run.started",
		Run:           RunSummary{SchemaVersion: SchemaVersion, RunID: "run_snap", StartedAt: time.Now().UTC()},
	})
	finished := writeEventLine(t, Event{
		SchemaVersion: SchemaVersion,
		EventType:     "run.finished",
		Outcome:       OutcomePassed,
		Run: RunSummary{
			SchemaVersion: SchemaVersion, RunID: "run_snap", StartedAt: time.Now().UTC(),
			Outcome: OutcomePassed, Broker: BrokerSummary{SessionID: "sess-1"},
		},
	})
	if err := os.WriteFile(path, []byte(started+"\n"+finished+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, err := ReadRunHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "run_snap" || runs[0].Outcome != OutcomePassed || runs[0].Broker.SessionID != "sess-1" {
		t.Fatalf("runs = %#v", runs)
	}
}

func TestReadRunHistoryIgnoresPartialCrashRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	valid := writeEventLine(t, Event{
		SchemaVersion: SchemaVersion,
		EventType:     "run.started",
		Run:           RunSummary{SchemaVersion: SchemaVersion, RunID: "run_valid", StartedAt: time.Now().UTC()},
	})
	data := valid + "\n" + `{"schema_version":`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, err := ReadRunHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "run_valid" {
		t.Fatalf("runs = %#v", runs)
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
