package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunCommandHelpDescribesContainerFirstFlow(t *testing.T) {
	if !strings.Contains(runCmd.Long, "devcontainer first") {
		t.Fatalf("run help text does not mention the container-first workflow")
	}
	if !strings.Contains(runCmd.Long, "inside the container") {
		t.Fatalf("run help text does not mention running ai-agent inside the container")
	}
}

func TestDevcontainerConfigMatchesSupportedFlow(t *testing.T) {
	root := repoRoot(t)

	devcontainerPath := filepath.Join(root, ".devcontainer", "devcontainer.json")
	devcontainer, err := os.ReadFile(devcontainerPath)
	if err != nil {
		t.Fatalf("read devcontainer.json: %v", err)
	}

	entrypointPath := filepath.Join(root, ".devcontainer", "entrypoint.sh")
	entrypoint, err := os.ReadFile(entrypointPath)
	if err != nil {
		t.Fatalf("read entrypoint.sh: %v", err)
	}

	dockerfile, err := os.ReadFile(filepath.Join(root, ".devcontainer", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}

	devcontainerText := string(devcontainer)
	entrypointText := string(entrypoint)
	dockerfileText := string(dockerfile)

	for _, want := range []string{
		`"workspaceMount": "source=${localEnv:AI_AGENT_WORKSPACE},target=/workspace,type=bind"`,
		`source=${localEnv:XDG_RUNTIME_DIR}/ai-agent,target=/run/ai-agent,type=bind`,
		`"AI_AGENT_AUTH_SOCK": "/run/ai-agent/broker.sock"`,
	} {
		if !strings.Contains(devcontainerText, want) {
			t.Fatalf("devcontainer config missing %q", want)
		}
	}

	// The unmanaged gh must be moved off PATH and pointed at by the wrapper,
	// so an agent cannot bypass the broker by invoking gh with personal auth.
	if strings.Contains(devcontainerText, "/usr/bin/gh") {
		t.Fatal("devcontainer must not expose the unmanaged /usr/bin/gh")
	}
	for _, want := range []string{
		"mv /usr/bin/gh",
		"ENV AI_AGENT_REAL_GH=/opt/ai-agent/bin/gh",
	} {
		if !strings.Contains(dockerfileText, want) {
			t.Fatalf("Dockerfile missing brokered-gh hardening %q", want)
		}
	}

	for _, unwanted := range []string{
		"session-bind",
		"AI_AGENT_SESSION_BIND_FD",
	} {
		if strings.Contains(devcontainerText, unwanted) {
			t.Fatalf("devcontainer config should not mention %q", unwanted)
		}
	}

	for _, want := range []string{
		"AI_AGENT_AUTH_SOCK",
		`exec "$@"`,
	} {
		if !strings.Contains(entrypointText, want) {
			t.Fatalf("entrypoint missing %q", want)
		}
	}

	for _, unwanted := range []string{
		"session-bind",
		"AI_AGENT_SESSION_BIND_FD",
	} {
		if strings.Contains(entrypointText, unwanted) {
			t.Fatalf("entrypoint should not mention %q", unwanted)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
