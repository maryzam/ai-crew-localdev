package readiness

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadinessDocsMatchRegistry(t *testing.T) {
	path := filepath.Join(repoRootForDocs(t), "docs", "guide", "cli-reference.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	begin := strings.Index(content, DocBeginMarker)
	end := strings.Index(content, DocEndMarker)
	if begin < 0 || end < 0 {
		t.Fatal("readiness-checks markers missing from cli-reference.md")
	}
	block := content[begin+len(DocBeginMarker)+1 : end]
	if block != DocMarkdown() {
		t.Fatalf("cli-reference readiness table drifted from the spec registry; run 'make readiness-docs'\n--- doc ---\n%s\n--- registry ---\n%s", block, DocMarkdown())
	}
}

func repoRootForDocs(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}
