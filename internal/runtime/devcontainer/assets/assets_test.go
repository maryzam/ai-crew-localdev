package assets

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const checkoutConfigDir = "../../../../.devcontainer"

func embeddedNames(t *testing.T) []string {
	t.Helper()
	generic, err := Generic()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(generic, ".")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestEmbeddedGenericAssetsMatchCheckout(t *testing.T) {
	generic, err := Generic()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range embeddedNames(t) {
		embeddedContent, err := fs.ReadFile(generic, name)
		if err != nil {
			t.Fatal(err)
		}
		checkoutContent, err := os.ReadFile(filepath.Join(checkoutConfigDir, name))
		if err != nil {
			t.Fatalf("%s is embedded but missing from .devcontainer/: %v", name, err)
		}
		if string(embeddedContent) != string(checkoutContent) {
			t.Fatalf("embedded %s drifted from .devcontainer/%s; run 'make devcontainer-assets'", name, name)
		}
	}

	entries, err := os.ReadDir(checkoutConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	embedded := strings.Join(embeddedNames(t), " ")
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if !strings.Contains(embedded, entry.Name()) {
			t.Fatalf(".devcontainer/%s is not embedded; run 'make devcontainer-assets'", entry.Name())
		}
	}
}

func TestGenericImageBuildsFromStagedBinaryNotSource(t *testing.T) {
	generic, err := Generic()
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := fs.ReadFile(generic, "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	content := string(dockerfile)
	if !strings.Contains(content, "COPY bin/ai-agent /usr/local/bin/ai-agent") {
		t.Fatal("generic image must install the staged ai-agent binary from the build context")
	}
	for _, sourceBuild := range []string{"COPY . .", "COPY go.mod", "RUN make build", "go build"} {
		if strings.Contains(content, sourceBuild) {
			t.Fatalf("generic image must not build from a source checkout (found %q); a released binary has no source tree", sourceBuild)
		}
	}
}
