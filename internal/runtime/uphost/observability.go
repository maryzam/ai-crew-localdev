package uphost

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
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
	phoenixArgs := []string{"run", "--detach", "--replace", "--pull=never", "--name", instance.phoenixContainer, "--network", instance.network, "--network-alias", "phoenix", "--restart", "unless-stopped", "--publish", "127.0.0.1:6006:6006", "--env", "PHOENIX_WORKING_DIR=/data", "--volume", instance.volume + ":/data", PhoenixImage}
	if err := runner(startContext, "podman", phoenixArgs, Streams{Out: streams.Out, Err: streams.Err}); err != nil {
		return fmt.Errorf("start Phoenix: %w", err)
	}
	if err := phoenixReady(startContext); err != nil {
		return fmt.Errorf("wait for Phoenix: %w", err)
	}
	collectorArgs := []string{"run", "--detach", "--replace", "--pull=never", "--name", instance.collectorContainer, "--user", "0:0", "--network", instance.network, "--restart", "unless-stopped", "--publish", "127.0.0.1:4319:4318", "--volume", collectorConfig + ":/etc/otelcol-contrib/collector.yaml:ro,Z", PhoenixCollectorImage, "--config=/etc/otelcol-contrib/collector.yaml"}
	if err := runner(startContext, "podman", collectorArgs, Streams{Out: streams.Out, Err: streams.Err}); err != nil {
		return fmt.Errorf("start Phoenix Collector: %w", err)
	}
	if err := collectorReady(startContext); err != nil {
		return fmt.Errorf("wait for Phoenix Collector: %w", err)
	}
	report(progress, Progress{Kind: PhoenixReady})
	return nil
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
		now := time.Now().UnixNano()
		payload := []byte(fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[{"key":"ai.project","value":{"stringValue":"ai-agent-readiness"}}]},"scopeSpans":[{"spans":[{"traceId":"ffffffffffffffffffffffffffffffff","spanId":"eeeeeeeeeeeeeeee","name":"ai-agent.phoenix.readiness","kind":1,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","status":{"code":1}}]}]}]}`, now, now+int64(time.Millisecond)))
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:4319/v1/traces", bytes.NewReader(payload))
		if err != nil {
			return false
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return false
		}
		response, err = client.Get("http://127.0.0.1:6006/v1/projects/ai-agent-readiness/spans")
		if err != nil {
			return false
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK && bytes.Contains(body, []byte("ai-agent.phoenix.readiness"))
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
