//go:build integration

package login

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
)

func TestPinnedLoginImageProvidesNativeCLIsAndRefreshBinary(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Fatal("podman is required")
	}
	var output bytes.Buffer
	registry := agents.Default()
	service := New(registry, Console{In: strings.NewReader(""), Out: &output, Err: &output})
	image, err := service.prepareImage(context.Background())
	if err != nil {
		t.Fatalf("prepare login image: %v\n%s", err, output.String())
	}
	if result, err := exec.Command("podman", "run", "--rm", "--network", "none", image.id, "test", "-s", "/etc/ssl/certs/ca-certificates.crt").CombinedOutput(); err != nil {
		t.Fatalf("login image has no system trust store for native TLS clients: %q, %v", result, err)
	}
	for _, agent := range registry.All() {
		result, err := exec.Command("podman", "run", "--rm", "--network", "none", image.id, agent.LoginCommand[0], "--version").CombinedOutput()
		if err != nil || !strings.Contains(string(result), agent.Version) {
			t.Fatalf("%s version = %q, %v", agent.Name, result, err)
		}
		if agent.Refresh == nil {
			continue
		}
		pin, err := service.refreshPin(agent)
		if err != nil {
			t.Fatal(err)
		}
		binaryDir := t.TempDir()
		paths := Paths{BinaryDir: binaryDir, Binary: filepath.Join(binaryDir, agent.Refresh.FileName)}
		if err := service.installRefreshBinary(context.Background(), agent, image, paths, pin); err != nil {
			t.Fatalf("%s refresh binary: %v", agent.Name, err)
		}
		result, err = exec.Command(paths.Binary, "--version").CombinedOutput()
		if err != nil || !strings.Contains(string(result), agent.Version) {
			t.Fatalf("%s refresh binary version = %q, %v", agent.Name, result, err)
		}
	}
}

func TestClaudeProbeIsANativePrintRequest(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Fatal("podman is required")
	}
	var output bytes.Buffer
	service := New(agents.Default(), Console{In: strings.NewReader(""), Out: &output, Err: &output})
	image, err := service.prepareImage(context.Background())
	if err != nil {
		t.Fatalf("prepare login image: %v\n%s", err, output.String())
	}
	args := append([]string{"run", "--rm", "--network", "none", "--env", "HOME=/tmp", "--env", "DISABLE_AUTOUPDATER=1", "--env", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", image.id}, claudeProbeCommand...)
	stdout, _ := exec.Command("podman", args...).Output()
	var result struct {
		IsError *bool `json:"is_error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &result); err != nil || result.IsError == nil || !*result.IsError {
		t.Fatalf("probe without a credential must be a failed native request, got %q", stdout)
	}
}
