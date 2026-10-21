//go:build integration

package login

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
)

func TestPinnedLoginImageProvidesNativeCLIsAndRefreshBinary(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Fatal("podman is required")
	}
	var output bytes.Buffer
	service := New(strings.NewReader(""), &output, &output)
	if err := service.ensureImage(context.Background()); err != nil {
		t.Fatalf("prepare login image: %v\n%s", err, output.String())
	}
	for _, agent := range agents.All() {
		command := exec.Command("podman", "run", "--rm", "--network", "none", agents.LoginImage, agent.LoginCommand[0], "--version")
		result, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(result), agent.Version) {
			t.Fatalf("%s version = %q, %v", agent.Name, result, err)
		}
	}
	command := exec.Command("podman", "run", "--rm", "--network", "none", agents.LoginImage, "/opt/ai-crew/codex", "--version")
	result, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(result), "0.163.0") {
		t.Fatalf("Codex refresh binary version = %q, %v", result, err)
	}
}
