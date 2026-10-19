//go:build integration && phoenix

package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	phoenixview "github.com/maryzam/ai-crew-localdev/internal/phoenix"
	"github.com/maryzam/ai-crew-localdev/internal/runtelemetry"
)

const phoenixCompose = "../../contrib/phoenix/docker-compose.yml"

func TestPhoenixBlackBox(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker is required")
	}
	project := fmt.Sprintf("ai-crew-phoenix-%d", os.Getpid())
	t.Cleanup(func() { compose(t, project, "down", "-v", "--remove-orphans") })
	compose(t, project, "up", "-d", "--wait")
	waitHTTP(t, "http://127.0.0.1:6006/healthz", http.StatusOK, "")
	requireExternalUnreachable(t, 6006)

	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	projector, err := phoenixview.NewProjector(phoenixview.DefaultCollectorEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	telemetryContext := runtelemetry.Context{RunID: "run-phoenix-blackbox", Task: "phoenix-test", Project: "ai-crew-blackbox", ImageDigest: "sha256:test", EgressMode: "open", Agents: map[runtelemetry.Agent]string{runtelemetry.Codex: "test"}}
	receiver, err := runtelemetry.NewReceiver(telemetryContext, root, runtelemetry.WithView(projector))
	if err != nil {
		t.Fatal(err)
	}
	postSpan(t, receiver, "phoenix-live-span", "00000000000000000000000000000001", "0000000000000001")
	waitHTTP(t, "http://127.0.0.1:6006/v1/projects/ai-crew-blackbox/spans", http.StatusOK, "phoenix-live-span")

	compose(t, project, "stop", "collector", "phoenix")
	postSpan(t, receiver, "phoenix-outage-span", "00000000000000000000000000000002", "0000000000000002")
	statsContext, cancel := context.WithCancel(context.Background())
	cancel()
	if err := receiver.RecordStats(statsContext, time.Hour); err != nil {
		t.Fatal(err)
	}
	summary, err := runtelemetry.Summarize(root, telemetryContext.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Records["spans"] != 2 || summary.Receiver == nil || summary.Receiver.ProjectionFailed != 1 {
		t.Fatalf("evidence after Phoenix outage = records %v receiver %+v", summary.Records, summary.Receiver)
	}

	compose(t, project, "up", "-d", "--wait")
	rebuilt, err := projector.Rebuild(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Projected != 2 || rebuilt.Failed != 0 {
		t.Fatalf("rebuild = %+v", rebuilt)
	}
	waitHTTP(t, "http://127.0.0.1:6006/v1/projects/ai-crew-blackbox/spans", http.StatusOK, "phoenix-outage-span")
}

func compose(t *testing.T, project string, args ...string) {
	t.Helper()
	commandArgs := []string{"compose", "-p", project, "-f", phoenixCompose}
	commandArgs = append(commandArgs, args...)
	command := exec.Command("docker", commandArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(commandArgs, " "), err, output)
	}
}

func postSpan(t *testing.T, receiver http.Handler, name, traceID, spanID string) {
	t.Helper()
	now := time.Now().UnixNano()
	span := map[string]any{
		"traceId": traceID, "spanId": spanID, "name": name, "kind": 1, "startTimeUnixNano": fmt.Sprint(now), "endTimeUnixNano": fmt.Sprint(now + int64(time.Millisecond)), "status": map[string]any{"code": 1},
	}
	payload := map[string]any{"resourceSpans": []any{
		map[string]any{"scopeSpans": []any{
			map[string]any{"spans": []any{span}},
		}},
	}}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, "http://run.sock/otlp/codex/v1/traces", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	receiver.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("receiver returned %d: %s", response.Code, response.Body.String())
	}
}

func waitHTTP(t *testing.T, endpoint string, status int, contains string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode == status && strings.Contains(string(body), contains) {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s did not return %d containing %q", endpoint, status, contains)
}

func requireExternalUnreachable(t *testing.T, port int) {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, networkInterface := range interfaces {
		addresses, _ := networkInterface.Addrs()
		for _, address := range addresses {
			parsed, _, err := net.ParseCIDR(address.String())
			if err != nil || parsed.IsLoopback() || parsed.To4() == nil {
				continue
			}
			connection, dialErr := net.DialTimeout("tcp", net.JoinHostPort(parsed.String(), fmt.Sprint(port)), 500*time.Millisecond)
			if connection != nil {
				_ = connection.Close()
			}
			if dialErr == nil {
				t.Fatalf("Phoenix accepted a connection through external address %s", parsed.String())
			}
			return
		}
	}
	t.Fatal("no non-loopback IPv4 address available for external reachability proof")
}
