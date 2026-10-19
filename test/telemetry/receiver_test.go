package telemetry

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/runtelemetry"
)

type receiverRun struct {
	context  runtelemetry.Context
	root     *evidence.Root
	receiver *runtelemetry.Receiver
	client   *http.Client
}

func hostContext(run string) runtelemetry.Context {
	return runtelemetry.Context{
		RunID: run, Task: "feature-a", Project: "demo", Repository: "example/demo", Branch: "ai/feature-a/x",
		ImageDigest: "sha256:abc", EgressMode: "open", Agents: map[runtelemetry.Agent]string{runtelemetry.Claude: "2.1.302", runtelemetry.Codex: "0.163.0"},
	}
}

func startReceiver(t *testing.T, root *evidence.Root, c runtelemetry.Context) *receiverRun {
	t.Helper()
	receiver, err := runtelemetry.NewReceiver(c, root)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(shortTempDir(t, "rr"), "run.sock")
	serveUnix(t, socket, receiver)
	return &receiverRun{context: c, root: root, receiver: receiver, client: unixClient(socket)}
}

func (r *receiverRun) post(t *testing.T, path, contentType string, body io.Reader, header map[string]string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://run"+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func logPayload(marker string) string {
	return `{"resourceLogs":[{"resource":{"attributes":[` +
		`{"key":"ai.run.id","value":{"stringValue":"forged"}},{"key":"ai.agent","value":{"stringValue":"codex"}},` +
		`{"key":"service.name","value":{"stringValue":"claude-code"}}]},` +
		`"scopeLogs":[{"logRecords":[{"timeUnixNano":"1792366433139000000","body":{"stringValue":"` + marker + `"},` +
		`"attributes":[{"key":"big","value":{"intValue":9007199254740993}}]}]}]}]}`
}

func storedLines(t *testing.T, r *receiverRun) [][]byte {
	t.Helper()
	path, err := r.root.TelemetryPath(r.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return readLines(t, path)
}

func TestReceiverStampsHostContextOverContainerClaimsAndKeepsTheRest(t *testing.T) {
	r := startReceiver(t, newRoot(t), hostContext("run-stamp"))
	if status := r.post(t, "/otlp/claude/v1/logs", "application/json", strings.NewReader(logPayload("hello <world> & more")), nil); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	lines := storedLines(t, r)
	if len(lines) != 1 {
		t.Fatalf("stored %d lines", len(lines))
	}
	want := stampedContext(r.context, runtelemetry.Claude)
	want["service.name"] = "claude-code"
	requireStampedContext(t, "stamp", decodeResources(t, lines), want)
	for _, kept := range []string{`"1792366433139000000"`, `9007199254740993`, `hello <world> & more`} {
		if !bytes.Contains(lines[0], []byte(kept)) {
			t.Errorf("stored record lost %s: %s", kept, lines[0])
		}
	}
	if bytes.Contains(lines[0], []byte("forged")) {
		t.Errorf("container-supplied context survived: %s", lines[0])
	}
}

func TestTelemetryStoredBeforeAReceiverRestartIsKept(t *testing.T) {
	root := newRoot(t)
	first := startReceiver(t, root, hostContext("run-restart"))
	if status := first.post(t, "/otlp/claude/v1/logs", "application/json", strings.NewReader(logPayload("before")), nil); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	second := startReceiver(t, root, hostContext("run-restart"))
	if status := second.post(t, "/otlp/claude/v1/logs", "application/json", strings.NewReader(logPayload("after")), nil); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	lines := storedLines(t, second)
	if len(lines) != 2 || !bytes.Contains(lines[0], []byte("before")) || !bytes.Contains(lines[1], []byte("after")) {
		t.Fatalf("acknowledged telemetry was not kept across a restart: %d lines", len(lines))
	}
}

func TestReceiverCountersSurviveRestart(t *testing.T) {
	root := newRoot(t)
	first := startReceiver(t, root, hostContext("run-counters"))
	if status := first.post(t, "/otlp/gemini/v1/logs", "application/json", strings.NewReader(logPayload("rejected")), nil); status != http.StatusNotFound {
		t.Fatalf("rejection status %d", status)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := first.receiver.RecordStats(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	if status := first.post(t, "/otlp/claude/v1/logs", "application/json", strings.NewReader(logPayload("before")), nil); status != http.StatusOK {
		t.Fatalf("first stored status %d", status)
	}
	if err := first.receiver.RecordStats(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	second := startReceiver(t, root, hostContext("run-counters"))
	if status := second.post(t, "/otlp/claude/v1/logs", "application/json", strings.NewReader(logPayload("after")), nil); status != http.StatusOK {
		t.Fatalf("second stored status %d", status)
	}
	if err := second.receiver.RecordStats(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	summary, err := runtelemetry.Summarize(root, "run-counters")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Receiver == nil || summary.Receiver.Stored != 2 || summary.Receiver.Rejected != 1 || summary.Receiver.Failed != 0 {
		t.Fatalf("counters across receiver restart: %+v", summary.Receiver)
	}
}

func TestPruningNeverHidesAcknowledgedTelemetry(t *testing.T) {
	root := newRoot(t)
	r := startReceiver(t, root, hostContext("run-prune"))
	const workers, perWorker, pruners = 4, 50, 3
	var acknowledged, wg sync.WaitGroup
	var mu sync.Mutex
	acked := 0
	done := make(chan struct{})
	wg.Add(pruners)
	for p := 0; p < pruners; p++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					if _, err := root.PruneTelemetry(0, nil); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}()
	}
	for w := 0; w < workers; w++ {
		acknowledged.Add(1)
		go func(w int) {
			defer acknowledged.Done()
			for i := 0; i < perWorker; i++ {
				if r.post(t, "/otlp/claude/v1/logs", "application/json", strings.NewReader(logPayload(fmt.Sprintf("m%02d-%03d", w, i))), nil) == http.StatusOK {
					mu.Lock()
					acked++
					mu.Unlock()
				}
			}
		}(w)
	}
	acknowledged.Wait()
	close(done)
	wg.Wait()

	reference := startReceiver(t, newRoot(t), hostContext("run-prune"))
	if reference.post(t, "/otlp/claude/v1/logs", "application/json", strings.NewReader(logPayload("m00-000")), nil) != http.StatusOK {
		t.Fatal("reference record refused")
	}
	lineBytes := int64(len(storedLines(t, reference)[0]) + 1)
	lines := storedLines(t, r)
	var prunedBytes, prunes int64
	f, err := os.Open(root.PruningPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var p evidence.PrunedTelemetry
		if err := json.Unmarshal(scanner.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		prunedBytes += p.Bytes
		prunes++
	}
	if prunes < 2 {
		t.Fatalf("only %d prunes ran during the writes; the scenario did not interleave pruning with acknowledged writes", prunes)
	}
	if prunedBytes%lineBytes != 0 {
		t.Fatalf("pruned %d bytes, not a whole number of %d byte records", prunedBytes, lineBytes)
	}
	if accounted := int(prunedBytes/lineBytes) + len(lines); accounted != acked {
		t.Fatalf("%d acknowledged records, but %d are stored or recorded as pruned", acked, accounted)
	}
}

func TestStorageFailuresAreReturnedToTheExporterCountedAndRecordedOnRecovery(t *testing.T) {
	root := newRoot(t)
	r := startReceiver(t, root, hostContext("run-failing"))
	ctx, cancel := context.WithCancel(context.Background())
	recorded := make(chan error, 1)
	go func() { recorded <- r.receiver.RecordStats(ctx, 10*time.Millisecond) }()
	runs := filepath.Join(root.Path(), evidence.RunsDir)
	if err := os.Chmod(runs, 0o500); err != nil {
		t.Fatal(err)
	}
	status := r.post(t, "/otlp/claude/v1/logs", "application/json", strings.NewReader(logPayload("lost")), nil)
	time.Sleep(50 * time.Millisecond)
	if err := os.Chmod(runs, 0o700); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-recorded; err != nil {
		t.Fatal(err)
	}
	if status < 500 {
		t.Fatalf("a storage failure was acknowledged with status %d", status)
	}
	summary, err := runtelemetry.Summarize(root, r.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Receiver == nil || summary.Receiver.Failed != 1 || summary.Receiver.Stored != 0 {
		t.Fatalf("storage failure not recorded: %+v", summary.Receiver)
	}
}

func TestReceiverRefusesRequestsOutsideTheTelemetryContract(t *testing.T) {
	r := startReceiver(t, newRoot(t), hostContext("run-refuse"))
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write([]byte(strings.Repeat(" ", runtelemetry.MaxRequestBytes+1)))
	_ = w.Close()
	cases := []struct {
		name, path, contentType string
		body                    io.Reader
		header                  map[string]string
		want                    int
	}{
		{"unknown agent", "/otlp/gemini/v1/logs", "application/json", strings.NewReader(logPayload("x")), nil, http.StatusNotFound},
		{"unknown signal", "/otlp/claude/v1/profiles", "application/json", strings.NewReader(logPayload("x")), nil, http.StatusNotFound},
		{"protobuf", "/otlp/claude/v1/logs", "application/x-protobuf", strings.NewReader("\x0a\x00"), nil, http.StatusUnsupportedMediaType},
		{"unknown encoding", "/otlp/claude/v1/logs", "application/json", strings.NewReader(logPayload("x")), map[string]string{"Content-Encoding": "br"}, http.StatusUnsupportedMediaType},
		{"oversized", "/otlp/claude/v1/logs", "application/json", strings.NewReader(strings.Repeat(" ", runtelemetry.MaxRequestBytes+1)), nil, http.StatusRequestEntityTooLarge},
		{"oversized after gzip", "/otlp/claude/v1/logs", "application/json", bytes.NewReader(gz.Bytes()), map[string]string{"Content-Encoding": "gzip"}, http.StatusRequestEntityTooLarge},
		{"logs on the traces route", "/otlp/claude/v1/traces", "application/json", strings.NewReader(logPayload("x")), nil, http.StatusBadRequest},
		{"not JSON", "/otlp/claude/v1/logs", "application/json", strings.NewReader("{"), nil, http.StatusBadRequest},
	}
	for _, c := range cases {
		if got := r.post(t, c.path, c.contentType, c.body, c.header); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
	if s := r.receiver.Stats(); s.Rejected != uint64(len(cases)) || s.Stored != 0 || s.Failed != 0 {
		t.Errorf("refusals not counted: %+v", s)
	}
	if lines := storedLines(t, r); len(lines) != 0 {
		t.Errorf("refused requests stored %d lines", len(lines))
	}
}
