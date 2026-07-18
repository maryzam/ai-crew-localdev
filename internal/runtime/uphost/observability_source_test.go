package uphost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func TestFindLangfuseComposeIgnoresAmbientCwd(t *testing.T) {
	hostile := t.TempDir()
	hostileCompose := filepath.Join(hostile, "contrib", "langfuse")
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

	got, err := findLangfuseCompose()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(got, hostile) {
		t.Fatalf("resolved compose from ambient cwd %q; must stay embedded-by-default", got)
	}
	if !strings.HasPrefix(got, dataDir) {
		t.Fatalf("resolved %q, expected embedded stage under %q", got, dataDir)
	}
}

func TestFindLangfuseComposeUsesExplicitTrustedCheckout(t *testing.T) {
	checkout := t.TempDir()
	trusted := filepath.Join(checkout, "contrib", "langfuse")
	if err := os.MkdirAll(trusted, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(trusted, "docker-compose.yml")
	if err := os.WriteFile(want, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvDevAssetsDir, checkout)

	got, err := findLangfuseCompose()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolved %q, want explicit trusted checkout %q", got, want)
	}
}
