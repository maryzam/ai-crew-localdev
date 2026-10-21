package runtelemetry

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

type blockingView struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (v *blockingView) Project(ctx context.Context, _ []byte) error {
	v.once.Do(func() { close(v.started) })
	select {
	case <-v.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestReceiverAcknowledgesStoredEvidenceBeforeProjectionFinishes(t *testing.T) {
	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	view := &blockingView{started: make(chan struct{}), release: make(chan struct{})}
	receiver, err := NewReceiver(Context{RunID: "run_async", Task: "task", Project: "project", ImageDigest: "unknown", EgressMode: "restricted", Agents: map[Agent]string{Codex: "unknown"}}, root, WithView(view))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- receiver.Store(Codex, "traces", []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{}]}]}]}`))
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("evidence acknowledgement waited for projection")
	}
	select {
	case <-view.started:
	case <-time.After(time.Second):
		t.Fatal("projection did not start")
	}
	close(view.release)
	if err := receiver.Close(); err != nil {
		t.Fatal(err)
	}
	stats := receiver.Stats()
	if stats.Stored != 1 || stats.Projected != 1 || stats.ProjectionFailed != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestReceiverMeasuresProjectionQueueOverflowAndShutdown(t *testing.T) {
	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	view := &blockingView{started: make(chan struct{}), release: make(chan struct{})}
	receiver, err := NewReceiver(Context{RunID: "run_bounded", Task: "task", Project: "project", ImageDigest: "unknown", EgressMode: "restricted", Agents: map[Agent]string{Codex: "unknown"}}, root, WithView(view))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{}]}]}]}`)
	if err := receiver.Store(Codex, "traces", payload); err != nil {
		t.Fatal(err)
	}
	select {
	case <-view.started:
	case <-time.After(time.Second):
		t.Fatal("projection did not start")
	}
	for range projectionQueueSize + 1 {
		if err := receiver.Store(Codex, "traces", payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := receiver.Close(); err != nil {
		t.Fatal(err)
	}
	close(view.release)
	stats := receiver.Stats()
	if stats.Stored != projectionQueueSize+2 || stats.Projected != 0 || stats.ProjectionFailed != projectionQueueSize+2 {
		t.Fatalf("stats = %+v", stats)
	}
}
