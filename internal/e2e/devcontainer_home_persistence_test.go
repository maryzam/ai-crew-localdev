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

func TestDevcontainerHomeVolumePersistsAcrossPodmanRestart(t *testing.T) {
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

	marker := fmt.Sprintf("home-volume-marker-%d", time.Now().UnixNano())
	if _, err := containerRuntime.Run(t, homePersistenceRunSpec(workspaceDir, runtimeDir, homeVolume, imageTag,
		[]string{
			"AI_AGENT_HOME_MARKER=" + marker,
		},
		"sh", "-c", "printf '%s' \"$AI_AGENT_HOME_MARKER\" > /home/dev/.ai-agent-home-persistence")); err != nil {
		t.Fatalf("write persistent home marker: %v", err)
	}

	out, err := containerRuntime.Run(t, homePersistenceRunSpec(workspaceDir, runtimeDir, homeVolume, imageTag, nil,
		"cat", "/home/dev/.ai-agent-home-persistence"))
	if err != nil {
		t.Fatalf("read persistent home marker: %v\n%s", err, string(out))
	}
	got := strings.TrimSpace(string(out))
	if got != marker {
		t.Fatalf("persistent home marker = %q, want %q", got, marker)
	}
}

func TestAgentCLIStateRootsPersistAcrossPodmanRestart(t *testing.T) {
	containerRuntime := newPodmanReadinessRuntime(t)

	imageTag := fmt.Sprintf("ai-agent-cli-state-persistence:%d", time.Now().UnixNano())
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

	volumeName := fmt.Sprintf("ai-agent-cli-state-%d", time.Now().UnixNano())
	homeVolume := containerRuntime.CreateVolume(t, volumeName)
	t.Cleanup(func() {
		containerRuntime.RemoveVolume(t, homeVolume)
	})

	claudeMarker := fmt.Sprintf("claude-state-%d", time.Now().UnixNano())
	codexMarker := fmt.Sprintf("codex-state-%d", time.Now().UnixNano())
	writeScript := strings.Join([]string{
		"set -eu",
		"mkdir -p /home/dev/.claude /home/dev/.codex",
		"printf '%s' \"$CLAUDE_MARKER\" > /home/dev/.claude/ai-agent-login-state-test",
		"printf '%s' \"$CODEX_MARKER\" > /home/dev/.codex/ai-agent-login-state-test",
		"chmod 700 /home/dev/.claude /home/dev/.codex",
	}, "\n")
	if _, err := containerRuntime.Run(t, homePersistenceRunSpec(workspaceDir, runtimeDir, homeVolume, imageTag,
		[]string{
			"CLAUDE_MARKER=" + claudeMarker,
			"CODEX_MARKER=" + codexMarker,
		},
		"sh", "-c", writeScript)); err != nil {
		t.Fatalf("write agent CLI state markers: %v", err)
	}

	readScript := strings.Join([]string{
		"set -eu",
		"cat /home/dev/.claude/ai-agent-login-state-test",
		"printf '\\n'",
		"cat /home/dev/.codex/ai-agent-login-state-test",
	}, "\n")
	out, err := containerRuntime.Run(t, homePersistenceRunSpec(workspaceDir, runtimeDir, homeVolume, imageTag, nil,
		"sh", "-c", readScript))
	if err != nil {
		t.Fatalf("read agent CLI state markers: %v\n%s", err, string(out))
	}
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(got) != 2 {
		t.Fatalf("agent CLI state marker output = %q, want two lines", string(out))
	}
	if got[0] != claudeMarker {
		t.Fatalf("Claude state marker = %q, want %q", got[0], claudeMarker)
	}
	if got[1] != codexMarker {
		t.Fatalf("Codex state marker = %q, want %q", got[1], codexMarker)
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
