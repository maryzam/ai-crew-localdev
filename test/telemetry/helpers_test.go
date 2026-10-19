package telemetry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/runtelemetry"
)

type resource struct {
	signal     string
	attributes map[string]string
}

type otlpResource struct {
	Resource struct {
		Attributes []struct {
			Key   string `json:"key"`
			Value struct {
				StringValue string `json:"stringValue"`
			} `json:"value"`
		} `json:"attributes"`
	} `json:"resource"`
}

func shortTempDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newRoot(t *testing.T) *evidence.Root {
	t.Helper()
	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func serveUnix(t *testing.T, path string, handler http.Handler) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
}

func unixClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}}
}

func readLines(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open stored telemetry: %v", err)
	}
	defer func() { _ = f.Close() }()
	var lines [][]byte
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 256<<20)
	for scanner.Scan() {
		if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 {
			lines = append(lines, append([]byte(nil), line...))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func decodeResources(t *testing.T, lines [][]byte) []resource {
	t.Helper()
	var out []resource
	for i, line := range lines {
		var payload struct {
			ResourceLogs    []otlpResource `json:"resourceLogs"`
			ResourceMetrics []otlpResource `json:"resourceMetrics"`
			ResourceSpans   []otlpResource `json:"resourceSpans"`
		}
		if err := json.Unmarshal(line, &payload); err != nil {
			t.Fatalf("decode stored OTLP line %d: %v", i+1, err)
		}
		for signal, groups := range map[string][]otlpResource{"logs": payload.ResourceLogs, "metrics": payload.ResourceMetrics, "spans": payload.ResourceSpans} {
			for _, g := range groups {
				attrs := map[string]string{}
				for _, kv := range g.Resource.Attributes {
					attrs[kv.Key] = kv.Value.StringValue
				}
				out = append(out, resource{signal, attrs})
			}
		}
	}
	return out
}

func stampedContext(c runtelemetry.Context, agent runtelemetry.Agent) map[string]string {
	return map[string]string{
		"ai.run.id":        c.RunID,
		"ai.task":          c.Task,
		"ai.agent":         string(agent),
		"ai.agent.version": c.Agents[agent],
		"ai.project":       c.Project,
		"ai.repository":    c.Repository,
		"ai.branch":        c.Branch,
		"ai.image.digest":  c.ImageDigest,
		"ai.egress.mode":   c.EgressMode,
	}
}

func requireStampedContext(t *testing.T, label string, resources []resource, want map[string]string) {
	t.Helper()
	if len(resources) == 0 {
		t.Errorf("%s stored no resources", label)
	}
	for _, res := range resources {
		for key, value := range want {
			if got := res.attributes[key]; got != value {
				t.Errorf("%s %s resource %s = %q, want %q", label, res.signal, key, got, value)
			}
		}
	}
}
