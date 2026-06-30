//go:build integration

package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDevcontainerCLIWorkflow(t *testing.T) {
	devcontainerBin, err := lookPath("devcontainer")
	if err != nil {
		t.Skipf("devcontainer CLI not available: %v", err)
	}

	containerRuntime := newPodmanReadinessRuntime(t)

	h := newReadinessHarness(t, containerRuntime)
	runtimeBin := containerRuntime.Binary()

	t.Setenv("AI_AGENT_WORKSPACE", h.rootDir)
	t.Setenv("XDG_RUNTIME_DIR", h.runtimeDir)

	repoRoot := repoRoot(t)

	t.Log("running devcontainer up")
	upCtx, upCancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer upCancel()

	upCmd := exec.CommandContext(upCtx, devcontainerBin,
		"up", "--docker-path", runtimeBin, "--workspace-folder", repoRoot)
	upOut, err := upCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("devcontainer up failed: %v\n%s", err, string(upOut))
	}
	t.Logf("devcontainer up output:\n%s", string(upOut))

	t.Cleanup(func() {
		downCtx, downCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer downCancel()

		out, err := exec.CommandContext(downCtx, runtimeBin,
			"ps", "-q", "--filter", "label=devcontainer.local_folder="+repoRoot).Output()
		if err == nil {
			for _, id := range strings.Fields(string(out)) {
				_ = exec.CommandContext(downCtx, runtimeBin, "rm", "-f", id).Run()
			}
		}
	})

	t.Log("running validation inside devcontainer")
	execCtx, execCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer execCancel()

	validationScript := `set -euo pipefail
echo "uid=$(id -u)"
echo "gid=$(id -g)"
test -S /run/ai-agent/broker.sock && echo "broker_socket=present" || echo "broker_socket=missing"
test -d /workspace && echo "workspace=mounted" || echo "workspace=missing"
which ai-agent && echo "ai-agent=found" || echo "ai-agent=missing"
which ai-agent-credential-helper && echo "credential-helper=found" || echo "credential-helper=missing"
which ai-agent-gh && echo "gh-wrapper=found" || echo "gh-wrapper=missing"
which git && echo "git=found" || echo "git=missing"
echo "GIT_TERMINAL_PROMPT=${GIT_TERMINAL_PROMPT:-unset}"
echo "done"
`

	execCmd := exec.CommandContext(execCtx, devcontainerBin,
		"exec", "--docker-path", runtimeBin, "--workspace-folder", repoRoot,
		"bash", "-c", validationScript)
	var execOut bytes.Buffer
	execCmd.Stdout = &execOut
	execCmd.Stderr = &execOut
	if err := execCmd.Run(); err != nil {
		t.Fatalf("devcontainer exec failed: %v\n%s", err, execOut.String())
	}

	output := execOut.String()
	t.Logf("validation output:\n%s", output)

	assertions := map[string]string{
		"workspace=mounted":       "workspace should be mounted",
		"ai-agent=found":          "ai-agent binary should be available",
		"credential-helper=found": "credential helper should be available",
		"gh-wrapper=found":        "gh wrapper should be available",
		"git=found":               "git should be available",
		"done":                    "validation script should complete",
	}

	for expected, msg := range assertions {
		if !strings.Contains(output, expected) {
			t.Errorf("%s: expected %q in output", msg, expected)
		}
	}

	if strings.Contains(output, "broker_socket=missing") {
		t.Error("broker socket not found inside container at /run/ai-agent/broker.sock")
	}

	envExecCmd := exec.CommandContext(execCtx, devcontainerBin,
		"exec", "--docker-path", runtimeBin, "--workspace-folder", repoRoot,
		"bash", "-c", "echo GH_TOKEN=${GH_TOKEN:-unset}; echo GITHUB_TOKEN=${GITHUB_TOKEN:-unset}")
	envOut, err := envExecCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("env check failed: %v\n%s", err, string(envOut))
	}
	envOutput := string(envOut)
	if !strings.Contains(envOutput, "GH_TOKEN=unset") {
		t.Logf("GH_TOKEN may be set in container (from host env): %s", envOutput)
	}
}

func init() {

	if os.Getenv("AI_AGENT_WORKSPACE") == "" {
		_ = os.Setenv("AI_AGENT_WORKSPACE", os.TempDir())
	}
}
