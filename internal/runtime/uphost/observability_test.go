package uphost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

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

func TestPreparePhoenixComposePinsImagesAndLoopbackPorts(t *testing.T) {
	composePath, err := preparePhoenixCompose(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)
	for _, expected := range []string{
		"arizephoenix/phoenix:version-20.23.0@sha256:",
		"otel/opentelemetry-collector-contrib:0.161.0@sha256:",
		`"127.0.0.1:6006:6006"`,
		`"127.0.0.1:4319:4318"`,
	} {
		if !strings.Contains(compose, expected) {
			t.Fatalf("Phoenix compose missing %q", expected)
		}
	}
	if strings.Contains(compose, "0.0.0.0:") {
		t.Fatal("Phoenix compose publishes a wildcard host port")
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
