package runtelemetry

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

const replayBatchBytes = 4 << 20

type ReplayResult struct {
	Runs      uint64
	Envelopes uint64
	Batches   uint64
	Failed    uint64
}

func Replay(ctx context.Context, root *evidence.Root, view View, selectedRun string) (ReplayResult, error) {
	result := ReplayResult{}
	runs, err := root.Runs()
	if err != nil {
		return result, err
	}
	if selectedRun != "" {
		found := false
		for _, run := range runs {
			if run == selectedRun {
				found = true
				break
			}
		}
		if !found {
			return result, fmt.Errorf("evidence run %q not found", selectedRun)
		}
		runs = []string{selectedRun}
	}
	for _, run := range runs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		path, err := root.TelemetryPath(run)
		if err != nil {
			return result, err
		}
		projected, replayErr := replayTraceFile(ctx, path, view, &result)
		if replayErr != nil {
			return result, fmt.Errorf("replay %s: %w", run, replayErr)
		}
		if projected {
			result.Runs++
		}
	}
	return result, nil
}

func replayTraceFile(ctx context.Context, path string, view View, result *ReplayResult) (bool, error) {
	var batch []json.RawMessage
	batchBytes := 0
	projected := false
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		payload, err := json.Marshal(struct {
			ResourceSpans []json.RawMessage `json:"resourceSpans"`
		}{ResourceSpans: batch})
		if err != nil {
			return err
		}
		if err := view.Project(ctx, payload); err != nil {
			result.Failed++
			return err
		}
		result.Batches++
		projected = true
		batch = nil
		batchBytes = 0
		return nil
	}
	err := eachLine(path, func(line []byte) error {
		var envelope struct {
			ResourceSpans []json.RawMessage `json:"resourceSpans"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			return err
		}
		if len(envelope.ResourceSpans) == 0 {
			return nil
		}
		result.Envelopes++
		for _, spans := range envelope.ResourceSpans {
			if batchBytes > 0 && batchBytes+len(spans) > replayBatchBytes {
				if err := flush(); err != nil {
					return err
				}
			}
			batch = append(batch, spans)
			batchBytes += len(spans)
		}
		return ctx.Err()
	})
	if err != nil {
		return projected, err
	}
	return projected, flush()
}
