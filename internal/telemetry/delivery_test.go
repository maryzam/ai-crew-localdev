package telemetry

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	if recorder == nil || recorder.Enabled() {
		t.Fatalf("recorder = %#v", recorder)
	}
	recorder.SetSessionID("session")
	recorder.AgentStarted(1)
	if recorder.Finish(OutcomePassed, PhaseAgent, nil, time.Second) || recorder.Finished() {
		t.Fatal("disabled recorder retained lifecycle state")
	}
	if recorder.Summary().RunID != "" || recorder.DeliveryStats() != (DeliveryStats{}) {
		t.Fatalf("disabled recorder state = %#v %#v", recorder.Summary(), recorder.DeliveryStats())
	}
	if recorder.DeliveryBudgets() != DefaultDeliveryBudgets() {
		t.Fatalf("budgets = %#v", recorder.DeliveryBudgets())
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
	if err == nil || recorder == nil || recorder.Enabled() {
		t.Fatalf("recorder=%#v error=%v", recorder, err)
	}
}

func TestDeliveryMetricsApplyBudgetsDeterministically(t *testing.T) {
	metrics := newDeliveryMetrics(DeliveryBudgets{MaxPayloadBytes: 4, MaxQueueDepth: 2, MaxExportLatency: 5 * time.Millisecond, MaxLocalWriteLatency: 3 * time.Millisecond})
	times := []time.Time{time.Unix(0, 0), time.Unix(0, int64(7*time.Millisecond)), time.Unix(0, int64(10*time.Millisecond)), time.Unix(0, int64(15*time.Millisecond))}
	metrics.now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}
	metrics.payload(5)
	metrics.queue(3)
	metrics.saturation(1)
	exportStart := metrics.started()
	metrics.exported(exportStart)
	writeStart := metrics.started()
	metrics.wroteLocal(writeStart)
	stats := metrics.snapshot()
	if stats.Payloads != 1 || stats.PayloadBytes != 5 || stats.MaxPayloadBytes != 5 || stats.DroppedEvents != 1 || stats.QueueSaturations != 1 || stats.MaxQueueDepth != 3 {
		t.Fatalf("counts = %#v", stats)
	}
	if stats.ExportLatency != 7*time.Millisecond || stats.LocalWriteLatency != 5*time.Millisecond {
		t.Fatalf("latencies = %#v", stats)
	}
	if stats.PayloadBudgetExceeded != 1 || stats.QueueBudgetExceeded != 2 || stats.ExportLatencyBudgetExceeded != 1 || stats.LocalWriteLatencyBudgetExceeded != 1 {
		t.Fatalf("budget counts = %#v", stats)
	}
}

func TestLocalWriteMeasurements(t *testing.T) {
	metrics := newDeliveryMetrics(DefaultDeliveryBudgets())
	times := []time.Time{time.Unix(0, 0), time.Unix(0, int64(4*time.Millisecond))}
	metrics.now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}
	sink, err := newLocalSinkMeasured(filepath.Join(t.TempDir(), "runs.jsonl"), 1<<20, metrics)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.write(representativeEvent()); err != nil {
		t.Fatal(err)
	}
	stats := metrics.snapshot()
	if stats.LocalWrites != 1 || stats.LocalWriteLatency != 4*time.Millisecond || stats.PayloadBytes == 0 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestOTLPExportMeasurements(t *testing.T) {
	metrics := newDeliveryMetrics(DefaultDeliveryBudgets())
	times := []time.Time{time.Unix(0, 0), time.Unix(0, int64(6*time.Millisecond))}
	metrics.now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}
	sink, err := newOTLPSinkMeasured(OTLPConfig{Endpoint: "http://example.test"}, metrics)
	if err != nil {
		t.Fatal(err)
	}
	sink.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	if err := sink.ingest([]Event{representativeEvent()}); err != nil {
		t.Fatal(err)
	}
	stats := metrics.snapshot()
	if stats.Exports != 1 || stats.ExportLatency != 6*time.Millisecond || stats.PayloadBytes == 0 || stats.DroppedEvents != 0 {
		t.Fatalf("stats = %#v", stats)
	}
}

func BenchmarkDeliveryMetricsSnapshot(b *testing.B) {
	metrics := newDeliveryMetrics(DefaultDeliveryBudgets())
	for b.Loop() {
		metrics.payload(1024)
		_ = metrics.snapshot()
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
		if err := sink.write(event); err != nil {
			b.Fatal(err)
		}
	}
}
