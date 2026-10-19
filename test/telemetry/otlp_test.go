//go:build integration

package telemetry

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type keyValue struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

type otlpResource struct {
	Attributes []keyValue `json:"attributes"`
}

type otlpRecord struct {
	Name       string       `json:"name"`
	EventName  string       `json:"eventName"`
	Body       *keyValueAny `json:"body"`
	Attributes []keyValue   `json:"attributes"`
	Events     []otlpRecord `json:"events"`
	Sum        *dataPoints  `json:"sum"`
	Gauge      *dataPoints  `json:"gauge"`
	Histogram  *dataPoints  `json:"histogram"`
}

type keyValueAny struct {
	StringValue string `json:"stringValue"`
}

type dataPoints struct {
	DataPoints []struct {
		Attributes []keyValue `json:"attributes"`
	} `json:"dataPoints"`
}

type otlpScope struct {
	LogRecords []otlpRecord `json:"logRecords"`
	Metrics    []otlpRecord `json:"metrics"`
	Spans      []otlpRecord `json:"spans"`
}

type otlpResourceGroup struct {
	Resource     otlpResource `json:"resource"`
	ScopeLogs    []otlpScope  `json:"scopeLogs"`
	ScopeMetrics []otlpScope  `json:"scopeMetrics"`
	ScopeSpans   []otlpScope  `json:"scopeSpans"`
}

type otlpPayload struct {
	ResourceLogs    []otlpResourceGroup `json:"resourceLogs"`
	ResourceMetrics []otlpResourceGroup `json:"resourceMetrics"`
	ResourceSpans   []otlpResourceGroup `json:"resourceSpans"`
}

type signalGroup struct {
	signal string
	group  otlpResourceGroup
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

func decodeLines(t *testing.T, lines [][]byte) []signalGroup {
	t.Helper()
	var groups []signalGroup
	for i, line := range lines {
		var payload otlpPayload
		if err := json.Unmarshal(line, &payload); err != nil {
			t.Fatalf("decode stored OTLP line %d: %v", i+1, err)
		}
		for _, g := range payload.ResourceLogs {
			groups = append(groups, signalGroup{"logs", g})
		}
		for _, g := range payload.ResourceMetrics {
			groups = append(groups, signalGroup{"metrics", g})
		}
		for _, g := range payload.ResourceSpans {
			groups = append(groups, signalGroup{"spans", g})
		}
	}
	return groups
}

func attribute(attrs []keyValue, key string) (string, bool) {
	for _, kv := range attrs {
		if kv.Key == key {
			if s, ok := kv.Value["stringValue"].(string); ok {
				return s, true
			}
			return fmt.Sprint(kv.Value), true
		}
	}
	return "", false
}

func (g otlpResourceGroup) records() []otlpRecord {
	var out []otlpRecord
	for _, scopes := range [][]otlpScope{g.ScopeLogs, g.ScopeMetrics, g.ScopeSpans} {
		for _, s := range scopes {
			out = append(out, s.LogRecords...)
			out = append(out, s.Metrics...)
			out = append(out, s.Spans...)
		}
	}
	return out
}

func (r otlpRecord) displayName() string {
	if name, ok := attribute(r.Attributes, "event.name"); ok {
		return name
	}
	if r.EventName != "" {
		return r.EventName
	}
	if r.Name != "" {
		return r.Name
	}
	if r.Body != nil {
		return r.Body.StringValue
	}
	return "<unnamed>"
}

func (r otlpRecord) attributeKeys() []string {
	attrs := append([]keyValue(nil), r.Attributes...)
	for _, points := range []*dataPoints{r.Sum, r.Gauge, r.Histogram} {
		if points == nil {
			continue
		}
		for _, p := range points.DataPoints {
			attrs = append(attrs, p.Attributes...)
		}
	}
	keys := map[string]bool{}
	for _, kv := range attrs {
		keys[kv.Key] = true
	}
	for _, e := range r.Events {
		keys["event:"+e.Name] = true
	}
	return sortedKeys(keys)
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type inventory map[string]map[string]map[string]bool

func inventoryOf(groups []signalGroup) inventory {
	inv := inventory{}
	for _, g := range groups {
		if inv[g.signal] == nil {
			inv[g.signal] = map[string]map[string]bool{}
		}
		for _, r := range g.group.records() {
			name := r.displayName()
			if inv[g.signal][name] == nil {
				inv[g.signal][name] = map[string]bool{}
			}
			for _, k := range r.attributeKeys() {
				inv[g.signal][name][k] = true
			}
		}
	}
	return inv
}

func (inv inventory) String() string {
	var b strings.Builder
	for _, signal := range []string{"logs", "metrics", "spans"} {
		names := map[string]bool{}
		for name := range inv[signal] {
			names[name] = true
		}
		for _, name := range sortedKeys(names) {
			fmt.Fprintf(&b, "%s %s: %s\n", signal, name, strings.Join(sortedKeys(inv[signal][name]), ", "))
		}
	}
	return b.String()
}

func presentSentinels(lines [][]byte, sentinels map[string]string) map[string]bool {
	present := map[string]bool{}
	for label, sentinel := range sentinels {
		for _, line := range lines {
			if bytes.Contains(line, []byte(sentinel)) {
				present[label] = true
				break
			}
		}
	}
	return present
}

func saveCapture(t *testing.T, name string, stored string, inv inventory) {
	t.Helper()
	dir := os.Getenv(captureDirEnv)
	if dir == "" {
		return
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(root, abs); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("%s must be outside the repository so raw captures are never committed", captureDirEnv)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abs, name+".otel.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abs, name+".inventory.txt"), []byte(inv.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}
