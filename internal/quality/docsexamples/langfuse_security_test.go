package docsexamples

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalLangfuseWebPortIsLoopbackOnly(t *testing.T) {
	path := filepath.Join(repoRoot(t), "contrib", "langfuse", "docker-compose.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)
	if !strings.Contains(compose, `"127.0.0.1:3000:3000"`) {
		t.Fatal("Langfuse web port must bind to loopback because bootstrap credentials are local defaults")
	}
}
