package runtelemetry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

type replayView struct {
	payloads [][]byte
}

func (v *replayView) Project(_ context.Context, payload []byte) error {
	v.payloads = append(v.payloads, append([]byte(nil), payload...))
	return nil
}

func TestReplayBatchesValidatedRunsAndCanSelectOne(t *testing.T) {
	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []string{"run-a", "run-b"} {
		if err := root.AppendTelemetry(run, []byte(`{"resourceLogs":[]}`)); err != nil {
			t.Fatal(err)
		}
		if err := root.AppendTelemetry(run, []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"name":"`+run+`"}]}]}]}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := root.AppendTelemetry("run-b", []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"name":"run-b-second"}]}]}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root.Path(), evidence.RunsDir, "stray entry"), 0o700); err != nil {
		t.Fatal(err)
	}
	view := &replayView{}
	result, err := Replay(context.Background(), root, view, "run-b")
	if err != nil {
		t.Fatal(err)
	}
	if result.Runs != 1 || result.Envelopes != 2 || result.Batches != 1 || result.Failed != 0 || len(view.payloads) != 1 {
		t.Fatalf("result = %+v payloads = %d", result, len(view.payloads))
	}
	var payload struct {
		ResourceSpans []json.RawMessage `json:"resourceSpans"`
	}
	if err := json.Unmarshal(view.payloads[0], &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.ResourceSpans) != 2 || string(payload.ResourceSpans[0]) == "" {
		t.Fatalf("payload = %s", view.payloads[0])
	}
}
