package telemetry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDisabledRecorderIsNonNilAndInert(t *testing.T) {
	t.Setenv("AI_AGENT_TELEMETRY", "disabled")
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if recorder == nil || !recorder.disabled {
		t.Fatalf("recorder = %#v", recorder)
	}
	recorder.SetSessionID("session")
	recorder.AgentStarted(1)
	if recorder.Finish(OutcomePassed, PhaseAgent, nil, time.Second) || recorder.Finished() {
		t.Fatal("disabled recorder retained lifecycle state")
	}
	if recorder.Summary().RunID != "" {
		t.Fatalf("disabled recorder state = %#v", recorder.Summary())
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("disabled telemetry created %s: %v", logPath, err)
	}
}

func TestStartRunErrorReturnsDisabledRecorder(t *testing.T) {
	recorder, err := StartRun(RunContext{RunID: "invalid", TaskRef: "not-a-task"})
	if err == nil || recorder == nil || !recorder.disabled {
		t.Fatalf("recorder=%#v error=%v", recorder, err)
	}
}

func BenchmarkLocalSinkWrite(b *testing.B) {
	sink, err := newLocalSinkSized(filepath.Join(b.TempDir(), "runs.jsonl"), 1<<30)
	if err != nil {
		b.Fatal(err)
	}
	event := representativeEvent()
	b.ResetTimer()
	for b.Loop() {
		if err := sink.Write(event); err != nil {
			b.Fatal(err)
		}
	}
}
