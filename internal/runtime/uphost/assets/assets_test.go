package assets

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

const checkoutLangfuseDir = "../../../../contrib/langfuse"

func TestEmbeddedLangfuseAssetsMatchCheckout(t *testing.T) {
	langfuse, err := Langfuse()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(langfuse, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("langfuse assets must be embedded; a released binary has no contrib/ checkout")
	}
	for _, entry := range entries {
		embeddedContent, err := fs.ReadFile(langfuse, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		checkoutContent, err := os.ReadFile(filepath.Join(checkoutLangfuseDir, entry.Name()))
		if err != nil {
			t.Fatalf("%s is embedded but missing from contrib/langfuse/: %v", entry.Name(), err)
		}
		if string(embeddedContent) != string(checkoutContent) {
			t.Fatalf("embedded %s drifted from contrib/langfuse/%s; run 'make langfuse-assets'", entry.Name(), entry.Name())
		}
	}
	for _, required := range []string{"docker-compose.yml", ".env.example"} {
		if _, err := fs.Stat(langfuse, required); err != nil {
			t.Fatalf("%s must be embedded for release installs: %v", required, err)
		}
	}
}
