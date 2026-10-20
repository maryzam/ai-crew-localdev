package uphost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func TestStartObservabilityUsesSelectedRuntimeForPullAndUp(t *testing.T) {
	t.Setenv(paths.EnvDataDir, t.TempDir())
	t.Setenv(paths.EnvDevAssetsDir, "")
	var commands [][]string
	runner := func(_ context.Context, name string, args []string, _ Streams) error {
		commands = append(commands, append([]string{name}, args...))
		return nil
	}
	if err := startObservability(context.Background(), "podman", Streams{}, nil, runner); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || commands[0][0] != "podman" || commands[0][len(commands[0])-1] != "pull" || commands[1][0] != "podman" || !strings.HasSuffix(strings.Join(commands[1], " "), "up -d --wait") {
		t.Fatalf("commands = %v", commands)
	}
}

func TestFindPhoenixComposeIgnoresAmbientWorkingDirectory(t *testing.T) {
	hostile := t.TempDir()
	hostileCompose := filepath.Join(hostile, "contrib", "phoenix")
	if err := os.MkdirAll(hostileCompose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostileCompose, "docker-compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(hostile)
	dataDir := t.TempDir()
	t.Setenv(paths.EnvDataDir, dataDir)
	t.Setenv(paths.EnvDevAssetsDir, "")
	got, err := findPhoenixCompose()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(got, hostile) || !strings.HasPrefix(got, dataDir) {
		t.Fatalf("resolved untrusted Phoenix compose path %q", got)
	}
}

func TestFindPhoenixComposeUsesExplicitTrustedCheckout(t *testing.T) {
	checkout := t.TempDir()
	dir := filepath.Join(checkout, "contrib", "phoenix")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(want, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvDevAssetsDir, checkout)
	got, err := findPhoenixCompose()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("compose path = %q, want %q", got, want)
	}
}

func TestPreparePhoenixComposeWritesOwnerOnlyAssets(t *testing.T) {
	composePath, err := preparePhoenixCompose(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docker-compose.yml", "collector.yaml"} {
		info, err := os.Stat(filepath.Join(filepath.Dir(composePath), name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", name, info.Mode().Perm())
		}
	}
}
