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
	requireExternalUnreachable(t, 6006, 4319)
	requireContainerUnreachable(t, project, 6006, 4319)

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
	waitReceiverStats(t, receiver, func(stats runtelemetry.Stats) bool { return stats.Projected == 1 })

	compose(t, project, "stop", "collector", "phoenix")
	postSpan(t, receiver, "phoenix-outage-span", "00000000000000000000000000000002", "0000000000000002")
	waitReceiverStats(t, receiver, func(stats runtelemetry.Stats) bool { return stats.ProjectionFailed == 1 })
	if err := receiver.Close(); err != nil {
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
	rebuilt, err := runtelemetry.Replay(context.Background(), root, projector, "")
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Envelopes != 2 || rebuilt.Batches != 1 || rebuilt.Failed != 0 {
		t.Fatalf("rebuild = %+v", rebuilt)
	}
	waitHTTP(t, "http://127.0.0.1:6006/v1/projects/ai-crew-blackbox/spans", http.StatusOK, "phoenix-outage-span")
	if _, err := runtelemetry.Replay(context.Background(), root, projector, ""); err != nil {
		t.Fatal(err)
	}
	waitHTTPCount(t, "http://127.0.0.1:6006/v1/projects/ai-crew-blackbox/spans", "phoenix-live-span", 1)
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

func waitHTTPCount(t *testing.T, endpoint, marker string, count int) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK && strings.Count(string(body), marker) == count {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s did not contain %q exactly %d time(s)", endpoint, marker, count)
}

func waitReceiverStats(t *testing.T, receiver *runtelemetry.Receiver, ready func(runtelemetry.Stats) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ready(receiver.Stats()) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("receiver stats did not converge: %+v", receiver.Stats())
}

func requireExternalUnreachable(t *testing.T, ports ...int) {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	addressesChecked := 0
	for _, networkInterface := range interfaces {
		addresses, _ := networkInterface.Addrs()
		for _, address := range addresses {
			parsed, _, err := net.ParseCIDR(address.String())
			if err != nil || parsed.IsLoopback() || parsed.To4() == nil {
				continue
			}
			addressesChecked++
			for _, port := range ports {
				connection, dialErr := net.DialTimeout("tcp", net.JoinHostPort(parsed.String(), fmt.Sprint(port)), 500*time.Millisecond)
				if connection != nil {
					_ = connection.Close()
				}
				if dialErr == nil {
					t.Fatalf("Phoenix accepted a connection through external address %s:%d", parsed.String(), port)
				}
			}
		}
	}
	if addressesChecked == 0 {
		t.Fatal("no non-loopback IPv4 address available for external reachability proof")
	}
}

func requireContainerUnreachable(t *testing.T, project string, ports ...int) {
	t.Helper()
	encoded, err := json.Marshal(ports)
	if err != nil {
		t.Fatal(err)
	}
	program := `import json,socket,struct,sys
ports=json.loads(sys.argv[1])
routes=open('/proc/net/route').read().splitlines()[1:]
gateways=[socket.inet_ntoa(struct.pack('<L',int(fields[2],16))) for fields in (line.split() for line in routes) if fields[1]=='00000000']
assert gateways
for gateway in gateways:
  for port in ports:
    sock=socket.socket()
    sock.settimeout(0.5)
    result=sock.connect_ex((gateway,port))
    sock.close()
    assert result != 0, f'{gateway}:{port} reachable'
`
	compose(t, project, "exec", "-T", "phoenix", "python", "-c", program, string(encoded))
}
