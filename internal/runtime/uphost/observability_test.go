package uphost

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func TestStartObservabilityUsesCachedPinnedImagesOffline(t *testing.T) {
	t.Setenv(paths.EnvDataDir, t.TempDir())
	t.Setenv(paths.EnvDevAssetsDir, "")
	var commands [][]string
	runner := func(_ context.Context, name string, args []string, _ Streams) error {
		commands = append(commands, append([]string{name}, args...))
		return nil
	}
	ready := func(context.Context) error { return nil }
	if err := startObservability(context.Background(), Streams{}, nil, runner, ready, ready, defaultObservabilityInstance); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if command[0] != "podman" {
			t.Fatalf("non-Podman command = %v", command)
		}
		if slices.Contains(command, "pull") {
			t.Fatalf("cached startup pulled an image: %v", commands)
		}
	}
	joined := commandLines(commands)
	for _, required := range []string{PhoenixImage, PhoenixCollectorImage, "127.0.0.1:6006:6006", "127.0.0.1:4319:4318", "--replace", "--user 0:0"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("commands missing %q: %s", required, joined)
		}
	}
}

func TestStartObservabilityPullsOnlyMissingImagesAndCreatesState(t *testing.T) {
	t.Setenv(paths.EnvDataDir, t.TempDir())
	t.Setenv(paths.EnvDevAssetsDir, "")
	var commands [][]string
	runner := func(_ context.Context, name string, args []string, _ Streams) error {
		commands = append(commands, append([]string{name}, args...))
		if len(args) == 3 && args[0] == "image" && args[1] == "exists" && args[2] == PhoenixCollectorImage {
			return errors.New("missing")
		}
		if len(args) == 3 && (args[0] == "network" || args[0] == "volume") && args[1] == "exists" {
			return errors.New("missing")
		}
		return nil
	}
	ready := func(context.Context) error { return nil }
	if err := startObservability(context.Background(), Streams{}, nil, runner, ready, ready, defaultObservabilityInstance); err != nil {
		t.Fatal(err)
	}
	joined := commandLines(commands)
	if strings.Contains(joined, "pull "+PhoenixImage) || !strings.Contains(joined, "pull "+PhoenixCollectorImage) {
		t.Fatalf("image pulls = %s", joined)
	}
	for _, required := range []string{"network create " + defaultObservabilityInstance.network, "volume create " + defaultObservabilityInstance.volume} {
		if !strings.Contains(joined, required) {
			t.Fatalf("commands missing %q: %s", required, joined)
		}
	}
}

func TestStartObservabilityKeepsRunningStackAndReplacesOnlyChangedContainers(t *testing.T) {
	checkout := t.TempDir()
	config := filepath.Join(checkout, "contrib", "phoenix", "collector.yaml")
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("receivers: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvDataDir, t.TempDir())
	t.Setenv(paths.EnvDevAssetsDir, checkout)
	running := map[string]string{}
	var runs []string
	runner := func(_ context.Context, name string, args []string, streams Streams) error {
		if len(args) > 0 && args[0] == "run" {
			name := args[slices.Index(args, "--name")+1]
			label := args[slices.Index(args, "--label")+1]
			running[name] = strings.TrimPrefix(label, phoenixSpecLabel+"=")
			runs = append(runs, name)
		}
		if len(args) > 1 && args[0] == "container" && args[1] == "inspect" {
			spec, ok := running[args[len(args)-1]]
			if !ok {
				return errors.New("no such container")
			}
			_, _ = io.WriteString(streams.Out, "true "+spec+"\n")
		}
		return nil
	}
	ready := func(context.Context) error { return nil }
	start := func() []string {
		runs = nil
		if err := startObservability(context.Background(), Streams{}, nil, runner, ready, ready, defaultObservabilityInstance); err != nil {
			t.Fatal(err)
		}
		return runs
	}
	instance := defaultObservabilityInstance
	if got := start(); !slices.Equal(got, []string{instance.phoenixContainer, instance.collectorContainer}) {
		t.Fatalf("first start ran %v", got)
	}
	if got := start(); len(got) != 0 {
		t.Fatalf("healthy stack was restarted: %v", got)
	}
	if err := os.WriteFile(config, []byte("receivers: {otlp: {}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := start(); !slices.Equal(got, []string{instance.collectorContainer}) {
		t.Fatalf("changed collector config restarted %v", got)
	}
}

func TestFindPhoenixCollectorConfigIgnoresAmbientWorkingDirectory(t *testing.T) {
	hostile := t.TempDir()
	hostileDir := filepath.Join(hostile, "contrib", "phoenix")
	if err := os.MkdirAll(hostileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostileDir, "collector.yaml"), []byte("hostile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(hostile)
	dataDir := t.TempDir()
	t.Setenv(paths.EnvDataDir, dataDir)
	t.Setenv(paths.EnvDevAssetsDir, "")
	got, err := findPhoenixCollectorConfig()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(got, hostile) || !strings.HasPrefix(got, dataDir) {
		t.Fatalf("resolved untrusted Phoenix asset path %q", got)
	}
}

func TestFindPhoenixCollectorConfigUsesExplicitTrustedCheckout(t *testing.T) {
	checkout := t.TempDir()
	dir := filepath.Join(checkout, "contrib", "phoenix")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "collector.yaml")
	if err := os.WriteFile(want, []byte("receivers: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvDevAssetsDir, checkout)
	got, err := findPhoenixCollectorConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("collector config path = %q, want %q", got, want)
	}
}

func TestPreparePhoenixAssetsWritesOwnerOnlyCollectorConfig(t *testing.T) {
	configPath, err := preparePhoenixAssets(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("collector config mode = %v, want 0600", info.Mode().Perm())
	}
}

func commandLines(commands [][]string) string {
	lines := make([]string, len(commands))
	for i, command := range commands {
		lines[i] = strings.Join(command, " ")
	}
	return strings.Join(lines, "\n")
}
