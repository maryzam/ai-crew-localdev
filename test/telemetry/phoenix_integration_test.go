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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	phoenixview "github.com/maryzam/ai-crew-localdev/internal/phoenix"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtelemetry"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/uphost"
)

func TestPhoenixBlackBox(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Fatal("podman is required")
	}
	t.Setenv(paths.EnvDataDir, t.TempDir())
	t.Setenv(paths.EnvDevAssetsDir, "")
	cleanupPhoenix(t)
	t.Cleanup(func() { cleanupPhoenix(t) })
	if err := uphost.StartObservability(context.Background(), uphost.Streams{Out: io.Discard, Err: io.Discard}, nil); err != nil {
		t.Fatalf("%v\ncollector:\n%s\nphoenix:\n%s", err, podmanOutput("logs", "--tail", "100", "ai-agent-phoenix-collector"), podmanOutput("logs", "--tail", "100", "ai-agent-phoenix"))
	}
	waitHTTP(t, "http://127.0.0.1:6006/healthz", http.StatusOK, "")
	requireExternalUnreachable(t, 6006, 4319)
	requireManagedContainerUnreachable(t, 6006, 4319)

	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	projector, err := phoenixview.NewProjector(phoenixview.DefaultCollectorEndpoint, phoenixview.LiveProjectionTimeout)
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
	postSpanWithPadding(t, receiver, "phoenix-large-span", "00000000000000000000000000000003", "0000000000000003", 2<<20)
	waitHTTP(t, "http://127.0.0.1:6006/v1/projects/ai-crew-blackbox/spans", http.StatusOK, "phoenix-large-span")
	waitReceiverStats(t, receiver, func(stats runtelemetry.Stats) bool { return stats.Projected == 2 })

	stopPodmanContainer(t, "ai-agent-phoenix-collector")
	stopPodmanContainer(t, "ai-agent-phoenix")
	postSpan(t, receiver, "phoenix-outage-span", "00000000000000000000000000000002", "0000000000000002")
	waitReceiverStats(t, receiver, func(stats runtelemetry.Stats) bool { return stats.ProjectionFailed == 1 })
	if err := receiver.Close(); err != nil {
		t.Fatal(err)
	}
	summary, err := runtelemetry.Summarize(root, telemetryContext.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Records["spans"] != 3 || summary.Receiver == nil || summary.Receiver.ProjectionFailed != 1 {
		t.Fatalf("evidence after Phoenix outage = records %v receiver %+v", summary.Records, summary.Receiver)
	}

	if err := uphost.StartObservability(context.Background(), uphost.Streams{Out: io.Discard, Err: io.Discard}, nil); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := runtelemetry.Replay(context.Background(), root, projector, "")
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Envelopes != 3 || rebuilt.Batches != 3 || rebuilt.Failed != 0 {
		t.Fatalf("rebuild = %+v", rebuilt)
	}
	waitHTTP(t, "http://127.0.0.1:6006/v1/projects/ai-crew-blackbox/spans", http.StatusOK, "phoenix-outage-span")
	if _, err := runtelemetry.Replay(context.Background(), root, projector, ""); err != nil {
		t.Fatal(err)
	}
	waitHTTPCount(t, "http://127.0.0.1:6006/v1/projects/ai-crew-blackbox/spans", "phoenix-live-span", 1)
}

func podman(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("podman", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("podman %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func podmanOutput(args ...string) string {
	output, _ := exec.Command("podman", args...).CombinedOutput()
	return string(output)
}

func stopPodmanContainer(t *testing.T, name string) {
	t.Helper()
	output, stopErr := exec.Command("podman", "stop", name).CombinedOutput()
	inspect, inspectErr := exec.Command("podman", "inspect", "--format", "{{.State.Running}}", name).CombinedOutput()
	if inspectErr == nil && strings.TrimSpace(string(inspect)) == "false" {
		return
	}
	if inspectErr != nil && stopErr != nil && strings.Contains(string(inspect), "no such") {
		return
	}
	if stopErr != nil {
		t.Fatalf("podman stop %s: %v\n%s\nstate: %s", name, stopErr, output, inspect)
	}
	t.Fatalf("podman stop left %s running: %s", name, inspect)
}

func cleanupPhoenix(t *testing.T) {
	t.Helper()
	for _, args := range [][]string{{"rm", "--force", "--ignore", "ai-agent-phoenix-collector", "ai-agent-phoenix"}, {"network", "rm", "--force", "ai-agent-phoenix"}, {"volume", "rm", "--force", "ai-agent-phoenix-data"}} {
		_ = exec.Command("podman", args...).Run()
	}
}

func postSpan(t *testing.T, receiver http.Handler, name, traceID, spanID string) {
	postSpanWithPadding(t, receiver, name, traceID, spanID, 0)
}

func postSpanWithPadding(t *testing.T, receiver http.Handler, name, traceID, spanID string, paddingBytes int) {
	t.Helper()
	now := time.Now().UnixNano()
	span := map[string]any{
		"traceId": traceID, "spanId": spanID, "name": name, "kind": 1, "startTimeUnixNano": fmt.Sprint(now), "endTimeUnixNano": fmt.Sprint(now + int64(time.Millisecond)), "status": map[string]any{"code": 1},
	}
	if paddingBytes > 0 {
		span["attributes"] = []any{map[string]any{"key": "large.payload", "value": map[string]any{"stringValue": strings.Repeat("x", paddingBytes)}}}
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

func requireManagedContainerUnreachable(t *testing.T, ports ...int) {
	t.Helper()
	encoded, err := json.Marshal(ports)
	if err != nil {
		t.Fatal(err)
	}
	program := `import json,socket,struct,sys
ports=json.loads(sys.argv[1])
routes=open('/proc/net/route').read().splitlines()[1:]
gateways=[socket.inet_ntoa(struct.pack('<L',int(fields[2],16))) for fields in (line.split() for line in routes) if fields[1]=='00000000']
targets=['127.0.0.1']+gateways
try:
  targets.extend(socket.gethostbyname_ex('host.containers.internal')[2])
except socket.gaierror:
  pass
for target in set(targets):
  for port in ports:
    sock=socket.socket()
    sock.settimeout(0.5)
    result=sock.connect_ex((target,port))
    sock.close()
    assert result != 0, f'{target}:{port} reachable'
`
	podman(t, "run", "--rm", "--pull=never", "--userns", "keep-id:uid=1000,gid=1000", "--user", "1000:1000", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--read-only", "--tmpfs=/tmp:rw,noexec,nosuid,size=16m", "--entrypoint", "python", uphost.PhoenixImage, "-c", program, string(encoded))
}
