package runtelemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

const (
	CollectorImage       = "docker.io/otel/opentelemetry-collector-contrib:0.161.0@sha256:fd328de2552466ad78385e1b1289c3f2402b1c45f265b252aab1955b42845ac1"
	DefaultCollectorName = "ai-crew-collector"
	UnattributedRun      = "_unattributed"
	CollectorStartBudget = 30 * time.Second
	collectorStopSeconds = 10
	collectorSocketName  = "otlp.sock"
	collectorConfigName  = "collector.yaml"
	collectorConfigLabel = "ai-crew.collector.config"
	containerRuntimeDir  = "/run/collector"
	containerEvidence    = "/evidence"
	selfMetricsEndpoint  = "127.0.0.1:4319"
	selfMetricsInterval  = 2 * time.Second
	fileFlushInterval    = "1s"
	maxUnixSocketPath    = 107
)

func CollectorConfig(socketPath, evidenceRoot string) ([]byte, error) {
	for _, p := range []string{socketPath, evidenceRoot} {
		if !path.IsAbs(p) {
			return nil, fmt.Errorf("collector path %q must be absolute", p)
		}
	}
	var attributes []map[string]any
	for _, key := range contextKeys() {
		attributes = append(attributes,
			map[string]any{"key": key, "action": "delete"},
			map[string]any{"key": key, "action": "insert", "from_context": "metadata." + headerName(key)},
		)
	}
	attributes = append(attributes, map[string]any{"key": RunIDAttribute, "action": "insert", "value": UnattributedRun})
	runPipeline := map[string]any{"receivers": []string{"otlp"}, "processors": []string{"resource/ai"}, "exporters": []string{"file/runs"}}
	config := map[string]any{
		"receivers": map[string]any{
			"otlp": map[string]any{"protocols": map[string]any{"http": map[string]any{
				"endpoint":              socketPath,
				"transport":             "unix",
				"include_metadata":      true,
				"max_request_body_size": MaxRequestBytes,
			}}},
			"otlp/self": map[string]any{"protocols": map[string]any{"http": map[string]any{"endpoint": selfMetricsEndpoint}}},
		},
		"processors": map[string]any{"resource/ai": map[string]any{"attributes": attributes}},
		"exporters": map[string]any{
			"file/runs": map[string]any{
				"path":           path.Join(evidenceRoot, evidence.RunsDir, "*", evidence.TelemetryFile),
				"flush_interval": fileFlushInterval,
				"group_by":       map[string]any{"enabled": true, "resource_attribute": RunIDAttribute},
			},
			"file/collector": map[string]any{
				"path":           path.Join(evidenceRoot, evidence.CollectorDir, evidence.CollectorMetricsFile),
				"flush_interval": fileFlushInterval,
			},
		},
		"service": map[string]any{
			"telemetry": map[string]any{
				"logs": map[string]any{"level": "warn"},
				"metrics": map[string]any{"level": "normal", "readers": []any{map[string]any{"periodic": map[string]any{
					"interval": selfMetricsInterval.Milliseconds(),
					"exporter": map[string]any{"otlp": map[string]any{"protocol": "http/protobuf", "endpoint": "http://" + selfMetricsEndpoint + "/v1/metrics"}},
				}}}},
			},
			"pipelines": map[string]any{
				"logs":         runPipeline,
				"metrics":      runPipeline,
				"traces":       runPipeline,
				"metrics/self": map[string]any{"receivers": []string{"otlp/self"}, "exporters": []string{"file/collector"}},
			},
		},
	}
	return json.MarshalIndent(config, "", "  ")
}

type Collector struct {
	Name       string
	RuntimeDir string
	Root       *evidence.Root
}

func (c Collector) Socket() string {
	return filepath.Join(c.RuntimeDir, collectorSocketName)
}

func (c Collector) Start(ctx context.Context) error {
	if c.Name == "" || c.Root == nil || !filepath.IsAbs(c.RuntimeDir) {
		return errors.New("collector needs a name, an evidence root, and an absolute runtime directory")
	}
	if len(c.Socket()) > maxUnixSocketPath {
		return fmt.Errorf("collector socket path %s exceeds %d bytes", c.Socket(), maxUnixSocketPath)
	}
	if err := os.MkdirAll(c.RuntimeDir, 0o700); err != nil {
		return fmt.Errorf("create collector runtime directory: %w", err)
	}
	if err := os.Chmod(c.RuntimeDir, 0o700); err != nil {
		return err
	}
	config, err := CollectorConfig(path.Join(containerRuntimeDir, collectorSocketName), containerEvidence)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(bytes.Join([][]byte{[]byte(CollectorImage), []byte(c.Root.Path()), []byte(c.RuntimeDir), config}, []byte{0}))
	fingerprint := hex.EncodeToString(digest[:])
	if c.running(ctx, fingerprint) && dialable(c.Socket()) {
		return nil
	}
	if _, err := podman(ctx, "rm", "-f", "-t", strconv.Itoa(collectorStopSeconds), "--ignore", c.Name); err != nil {
		return fmt.Errorf("remove previous collector: %w", err)
	}
	if err := os.Remove(c.Socket()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale collector socket: %w", err)
	}
	if err := securefile.WriteOwnerOnly(filepath.Join(c.RuntimeDir, collectorConfigName), config); err != nil {
		return fmt.Errorf("write collector config: %w", err)
	}
	user := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	if _, err := podman(ctx, "run", "-d", "--name", c.Name, "--label", collectorConfigLabel+"="+fingerprint,
		"--restart", "on-failure", "--network", "none", "--userns", "keep-id", "--user", user,
		"-v", c.RuntimeDir+":"+containerRuntimeDir, "-v", c.Root.Path()+":"+containerEvidence,
		CollectorImage, "--config", path.Join(containerRuntimeDir, collectorConfigName)); err != nil {
		return fmt.Errorf("start collector: %w", err)
	}
	deadline := time.Now().Add(CollectorStartBudget)
	for !dialable(c.Socket()) {
		if time.Now().After(deadline) || ctx.Err() != nil {
			logs, _ := podman(context.Background(), "logs", "--tail", "20", c.Name)
			_, _ = podman(context.Background(), "rm", "-f", "-t", "0", "--ignore", c.Name)
			return fmt.Errorf("collector did not accept connections within %s:\n%s", CollectorStartBudget, logs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (c Collector) Stop(ctx context.Context) error {
	_, err := podman(ctx, "rm", "-f", "-t", strconv.Itoa(collectorStopSeconds), "--ignore", c.Name)
	return err
}

func (c Collector) running(ctx context.Context, fingerprint string) bool {
	out, err := podman(ctx, "container", "inspect", "--format", `{{.State.Running}} {{index .Config.Labels "`+collectorConfigLabel+`"}}`, c.Name)
	return err == nil && strings.TrimSpace(out) == "true "+fingerprint
}

func dialable(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func podman(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "podman", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("podman %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
