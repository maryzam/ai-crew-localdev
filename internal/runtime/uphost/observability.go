package uphost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/embedasset"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/assetsource"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/uphost/assets"
)

const (
	PhoenixImage          = "docker.io/arizephoenix/phoenix:version-20.23.0@sha256:c542e7de1dc1fdd0f938de337d44ebefa7266e91391eed6e7978523d7f6e1f2f"
	PhoenixCollectorImage = "docker.io/otel/opentelemetry-collector-contrib:0.161.0@sha256:fd328de2552466ad78385e1b1289c3f2402b1c45f265b252aab1955b42845ac1"
	phoenixPullBudget     = 5 * time.Minute
	phoenixStartBudget    = 90 * time.Second
	phoenixSpecLabel      = "ai-agent.spec"
)

type observabilityInstance struct {
	network            string
	volume             string
	phoenixContainer   string
	collectorContainer string
}

var defaultObservabilityInstance = observabilityInstance{
	network:            "ai-agent-phoenix",
	volume:             "ai-agent-phoenix-data",
	phoenixContainer:   "ai-agent-phoenix",
	collectorContainer: "ai-agent-phoenix-collector",
}

type observabilityWaiter func(context.Context) error

func StartObservability(ctx context.Context, streams Streams, progress ProgressFunc) error {
	return startObservability(ctx, streams, progress, runCommand, waitForPhoenix, waitForCollector, defaultObservabilityInstance)
}

func startObservability(ctx context.Context, streams Streams, progress ProgressFunc, runner CommandRunner, phoenixReady, collectorReady observabilityWaiter, instance observabilityInstance) error {
	collectorConfig, err := findPhoenixCollectorConfig()
	if err != nil {
		return err
	}
	collectorConfigContent, err := os.ReadFile(collectorConfig)
	if err != nil {
		return fmt.Errorf("read Phoenix Collector config: %w", err)
	}
	report(progress, Progress{Kind: PhoenixStarting})
	pullContext, cancelPull := context.WithTimeout(ctx, phoenixPullBudget)
	defer cancelPull()
	for _, image := range []string{PhoenixImage, PhoenixCollectorImage} {
		if runner(pullContext, "podman", []string{"image", "exists", image}, Streams{}) == nil {
			continue
		}
		if err := runner(pullContext, "podman", []string{"pull", image}, Streams{Out: streams.Out, Err: streams.Err}); err != nil {
			return fmt.Errorf("podman pull %s: %w", image, err)
		}
	}
	startContext, cancelStart := context.WithTimeout(ctx, phoenixStartBudget)
	defer cancelStart()
	if err := ensurePodmanObject(startContext, runner, "network", instance.network, streams); err != nil {
		return err
	}
	if err := ensurePodmanObject(startContext, runner, "volume", instance.volume, streams); err != nil {
		return err
	}
	phoenixSpec := []string{"--network", instance.network, "--network-alias", "phoenix", "--restart", "unless-stopped", "--publish", "127.0.0.1:6006:6006", "--env", "PHOENIX_WORKING_DIR=/data", "--volume", instance.volume + ":/data", PhoenixImage}
	if err := ensureContainer(startContext, runner, instance.phoenixContainer, phoenixSpec, streams); err != nil {
		return fmt.Errorf("start Phoenix: %w", err)
	}
	if err := phoenixReady(startContext); err != nil {
		return fmt.Errorf("wait for Phoenix: %w", err)
	}
	collectorSpec := []string{"--user", "0:0", "--network", instance.network, "--restart", "unless-stopped", "--publish", "127.0.0.1:4319:4318", "--volume", collectorConfig + ":/etc/otelcol-contrib/collector.yaml:ro,Z", PhoenixCollectorImage, "--config=/etc/otelcol-contrib/collector.yaml"}
	if err := ensureContainer(startContext, runner, instance.collectorContainer, collectorSpec, streams, collectorConfigContent); err != nil {
		return fmt.Errorf("start Phoenix Collector: %w", err)
	}
	if err := collectorReady(startContext); err != nil {
		return fmt.Errorf("wait for Phoenix Collector: %w", err)
	}
	report(progress, Progress{Kind: PhoenixReady})
	return nil
}

func ensureContainer(ctx context.Context, runner CommandRunner, name string, spec []string, streams Streams, inputs ...[]byte) error {
	digest := containerSpecDigest(spec, inputs...)
	var state bytes.Buffer
	inspect := []string{"container", "inspect", "--format", `{{.State.Running}} {{index .Config.Labels "` + phoenixSpecLabel + `"}}`, name}
	if runner(ctx, "podman", inspect, Streams{Out: &state}) == nil && strings.TrimSpace(state.String()) == "true "+digest {
		return nil
	}
	args := append([]string{"run", "--detach", "--replace", "--pull=never", "--name", name, "--label", phoenixSpecLabel + "=" + digest}, spec...)
	return runner(ctx, "podman", args, Streams{Out: streams.Out, Err: streams.Err})
}

func containerSpecDigest(spec []string, inputs ...[]byte) string {
	hash := sha256.New()
	for _, part := range spec {
		hash.Write([]byte(part))
		hash.Write([]byte{0})
	}
	for _, input := range inputs {
		hash.Write(input)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func ensurePodmanObject(ctx context.Context, runner CommandRunner, kind, name string, streams Streams) error {
	if runner(ctx, "podman", []string{kind, "exists", name}, Streams{}) == nil {
		return nil
	}
	if err := runner(ctx, "podman", []string{kind, "create", name}, Streams{Out: streams.Out, Err: streams.Err}); err != nil {
		return fmt.Errorf("create Phoenix %s: %w", kind, err)
	}
	return nil
}

func waitForPhoenix(ctx context.Context) error {
	client := &http.Client{Timeout: time.Second}
	return waitForEndpoint(ctx, func() bool {
		response, err := client.Get("http://127.0.0.1:6006/healthz")
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	})
}

func waitForCollector(ctx context.Context) error {
	client := &http.Client{Timeout: time.Second}
	return waitForEndpoint(ctx, func() bool {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:4319/v1/traces", strings.NewReader(`{"resourceSpans":[]}`))
		if err != nil {
			return false
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	})
}

func waitForEndpoint(ctx context.Context, ready func() bool) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func findPhoenixCollectorConfig() (string, error) {
	if dir, ok := assetsource.TrustedCheckoutDir(); ok {
		candidate := filepath.Join(dir, "contrib", "phoenix", "collector.yaml")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return preparePhoenixAssets(paths.DataDir())
}

func preparePhoenixAssets(dataDir string) (string, error) {
	phoenix, err := assets.Phoenix()
	if err != nil {
		return "", fmt.Errorf("load Phoenix assets: %w", err)
	}
	dir := filepath.Join(dataDir, assets.PhoenixDir)
	if err := embedasset.Materialize(phoenix, dir, func(string) fs.FileMode { return 0o600 }); err != nil {
		return "", err
	}
	return filepath.Join(dir, "collector.yaml"), nil
}

func report(sink ProgressFunc, progress Progress) {
	if sink != nil {
		sink(progress)
	}
}
