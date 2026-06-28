package telemetry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadRunHistorySupportsLegacyEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	data := "" +
		`{"timestamp":"2026-06-26T01:00:00Z","run_id":"run_legacy","event_type":"run.started","agent_name":"codex","repo":"owner/repo","model":"gpt-5"}` + "\n" +
		`{"timestamp":"2026-06-26T01:00:01Z","run_id":"run_legacy","session_id":"sess-1","event_type":"session.created","agent_name":"codex","repo":"owner/repo","model":"gpt-5"}` + "\n" +
		`{"timestamp":"2026-06-26T01:00:02Z","run_id":"run_legacy","session_id":"sess-1","event_type":"run.finished","agent_name":"codex","repo":"owner/repo","model":"gpt-5","outcome":"passed","exit_code":0,"duration_ms":2000}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, err := ReadRunHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "run_legacy" || runs[0].Outcome != "passed" {
		t.Fatalf("runs = %#v", runs)
	}
	if runs[0].Model.Requested != "gpt-5" || runs[0].Broker.SessionID != "sess-1" {
		t.Fatalf("legacy summary = %#v", runs[0])
	}
}

func TestFindRunRejectsAmbiguousPrefix(t *testing.T) {
	runs := []RunSummary{{RunID: "run_abc111"}, {RunID: "run_abc222"}}
	if _, err := FindRun(runs, "abc"); err == nil {
		t.Fatal("ambiguous run prefix accepted")
	}
}

func TestReadRunHistoryIgnoresPartialCrashRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	data := `{"timestamp":"2026-06-26T01:00:00Z","run_id":"run_valid","event_type":"run.started","agent_name":"codex","repo":"owner/repo"}` + "\n" + `{"timestamp":`
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

func TestApplyEventKeepsUsageAcrossLaterEvents(t *testing.T) {
	total := int64(42)
	runs := make(map[string]RunSummary)
	applyEvent(runs, Event{SchemaVersion: SchemaVersion, RunID: "run_usage", EventType: "run.started"})
	applyEvent(runs, Event{SchemaVersion: SchemaVersion, RunID: "run_usage", EventType: "usage.recorded", Usage: &Usage{Status: "estimated", TotalTokens: &total}})
	applyEvent(runs, Event{SchemaVersion: SchemaVersion, RunID: "run_usage", EventType: "session.revoked"})

	usage := runs["run_usage"].Usage
	if usage == nil || usage.TotalTokens == nil || *usage.TotalTokens != total {
		t.Fatalf("usage lost after later event: %#v", usage)
	}
}
