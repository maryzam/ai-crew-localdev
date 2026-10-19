package docsexamples

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalPhoenixPortsAreLoopbackOnly(t *testing.T) {
	path := filepath.Join(repoRoot(t), "contrib", "phoenix", "docker-compose.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)
	for _, binding := range []string{`"127.0.0.1:6006:6006"`, `"127.0.0.1:4319:4318"`} {
		if !strings.Contains(compose, binding) {
			t.Fatalf("Phoenix runtime port %s must bind to loopback", binding)
		}
	}
}
