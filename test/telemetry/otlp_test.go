//go:build integration

package telemetry

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"testing"
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
