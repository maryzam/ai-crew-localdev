package runtelemetry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

type replayView struct {
	payloads [][]byte
}

func TestReplaySplitsLargeEvidenceIntoBoundedBatches(t *testing.T) {
	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat("x", replayBatchBytes/2+1)
	for i := range 3 {
		line, err := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{"resource": map[string]any{"attributes": []any{map[string]any{"key": "padding", "value": map[string]any{"stringValue": padding}}}}, "scopeSpans": []any{map[string]any{"spans": []any{map[string]any{"name": i}}}}}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := root.AppendTelemetry("run-large", line); err != nil {
			t.Fatal(err)
		}
	}
	view := &replayView{}
	result, err := Replay(context.Background(), root, view, "run-large")
	if err != nil {
		t.Fatal(err)
	}
	if result.Envelopes != 3 || result.Batches != 3 || len(view.payloads) != 3 {
		t.Fatalf("result = %+v payloads = %d", result, len(view.payloads))
	}
	for _, payload := range view.payloads {
		if len(payload) > replayBatchBytes+1024 {
			t.Fatalf("replay batch = %d bytes", len(payload))
		}
	}
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
