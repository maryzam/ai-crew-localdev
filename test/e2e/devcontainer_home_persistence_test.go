//go:build integration

package e2e

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexLoginPersistsAcrossContainerReplacement(t *testing.T) {
	containerRuntime := newPodmanReadinessRuntime(t)

	imageTag := fmt.Sprintf("ai-agent-home-persistence:%d", time.Now().UnixNano())
	containerRuntime.BuildImage(t, imageTag)
	t.Cleanup(func() {
		containerRuntime.RemoveImage(t, imageTag)
	})

	testDir := t.TempDir()
	workspaceDir := filepath.Join(testDir, "workspace")
	runtimeDir := filepath.Join(testDir, "runtime", "ai-agent")
	for _, dir := range []string{workspaceDir, runtimeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	socketPath := filepath.Join(runtimeDir, "broker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen broker socket: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatalf("chmod broker socket: %v", err)
	}
	go acceptAndClose(listener)

	volumeName := fmt.Sprintf("ai-agent-home-persistence-%d", time.Now().UnixNano())
	homeVolume := containerRuntime.CreateVolume(t, volumeName)
	t.Cleanup(func() {
		containerRuntime.RemoveVolume(t, homeVolume)
	})

	const testAPIKey = "sk-test-ai-agent-login-persistence"
	loginScript := "printf '%s\\n' \"$CODEX_TEST_API_KEY\" | codex login --with-api-key"
	loginOut, err := containerRuntime.Run(t, homePersistenceRunSpec(workspaceDir, runtimeDir, homeVolume, imageTag,
		[]string{"CODEX_TEST_API_KEY=" + testAPIKey}, "sh", "-c", loginScript))
	if err != nil {
		t.Fatalf("codex login failed: %v\n%s", err, string(loginOut))
	}
	if strings.Contains(string(loginOut), testAPIKey) {
		t.Fatal("codex login echoed the API key")
	}

	out, err := containerRuntime.Run(t, homePersistenceRunSpec(workspaceDir, runtimeDir, homeVolume, imageTag, nil,
		"codex", "login", "status"))
	if err != nil {
		t.Fatalf("codex login status failed after container replacement: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "Logged in using an API key") {
		t.Fatalf("codex did not reuse persisted login state: %s", string(out))
	}
}

func homePersistenceRunSpec(workspaceDir string, runtimeDir string, homeVolume string, imageTag string, env []string, command ...string) readinessRunSpec {
	baseEnv := []string{
		"AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock",
		"HOME=" + readinessHomeDir,
	}
	baseEnv = append(baseEnv, env...)
	return readinessRunSpec{
		Env: baseEnv,
		Mounts: []readinessMount{
			{Source: workspaceDir, Target: "/workspace", Relabel: true},
			{Source: runtimeDir, Target: "/run/ai-agent", Relabel: true},
			{Source: homeVolume, Target: readinessHomeDir},
		},
		Image:   imageTag,
		Command: command,
	}
}

func acceptAndClose(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}
