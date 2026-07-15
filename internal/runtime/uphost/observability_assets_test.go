package uphost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareLangfuseComposeNeedsNoCheckout(t *testing.T) {
	dataDir := t.TempDir()

	composePath, err := prepareLangfuseCompose(dataDir)
	if err != nil {
		t.Fatalf("prepare langfuse compose: %v", err)
	}
	if want := filepath.Join(dataDir, "langfuse", "docker-compose.yml"); composePath != want {
		t.Fatalf("compose path = %q, want %q", composePath, want)
	}
	for _, name := range []string{"docker-compose.yml", ".env.example"} {
		info, err := os.Stat(filepath.Join(dataDir, "langfuse", name))
		if err != nil {
			t.Fatalf("%s missing from prepared stack: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", name, info.Mode().Perm())
		}
	}
}

func TestPrepareLangfuseComposeKeepsExistingSecrets(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := prepareLangfuseCompose(dataDir); err != nil {
		t.Fatalf("prepare langfuse compose: %v", err)
	}
	envPath := filepath.Join(dataDir, "langfuse", ".env")
	if err := os.WriteFile(envPath, []byte("LANGFUSE_INIT_PROJECT_ID=kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareLangfuseCompose(dataDir); err != nil {
		t.Fatalf("re-prepare langfuse compose: %v", err)
	}
	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "LANGFUSE_INIT_PROJECT_ID=kept\n" {
		t.Fatalf(".env = %q, want the operator's existing secrets preserved", content)
	}
}
