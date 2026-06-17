//go:build integration

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDevcontainerHomeVolumePersistsAcrossPodmanRestart(t *testing.T) {
	podmanBin, err := exec.LookPath("podman")
	if err != nil {
		t.Skipf("podman not available: %v", err)
	}

	root := repoRoot(t)
	imageTag := fmt.Sprintf("ai-agent-home-persistence:%d", time.Now().UnixNano())
	mustRunOutput(t, 20*time.Minute, root, podmanBin, "build", "--quiet", "-f", ".devcontainer/Dockerfile", "-t", imageTag, ".")
	t.Cleanup(func() {
		_, _ = runOutput(2*time.Minute, root, podmanBin, "rmi", "-f", imageTag)
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
	mustRunOutput(t, time.Minute, root, podmanBin, "volume", "create", volumeName)
	t.Cleanup(func() {
		_, _ = runOutput(time.Minute, root, podmanBin, "volume", "rm", "-f", volumeName)
	})

	marker := fmt.Sprintf("home-volume-marker-%d", time.Now().UnixNano())
	writeArgs := hardenedPodmanRunArgs(workspaceDir, runtimeDir, volumeName, imageTag,
		"sh", "-c", "printf '%s' \"$AI_AGENT_HOME_MARKER\" > /home/dev/.ai-agent-home-persistence")
	writeArgs = append([]string{"run", "--rm", "-e", "AI_AGENT_HOME_MARKER=" + marker}, writeArgs...)
	mustRunOutput(t, 2*time.Minute, root, podmanBin, writeArgs...)

	readArgs := append([]string{"run", "--rm"}, hardenedPodmanRunArgs(workspaceDir, runtimeDir, volumeName, imageTag,
		"cat", "/home/dev/.ai-agent-home-persistence")...)
	out := mustRunOutput(t, 2*time.Minute, root, podmanBin, readArgs...)
	if strings.TrimSpace(out) != marker {
		t.Fatalf("persistent home marker = %q, want %q", strings.TrimSpace(out), marker)
	}
}

func hardenedPodmanRunArgs(workspaceDir string, runtimeDir string, volumeName string, imageTag string, command ...string) []string {
	args := []string{
		"--userns=keep-id",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--read-only",
		"--tmpfs=/tmp:rw,noexec,nosuid,size=512m",
		"-v", workspaceDir + ":/workspace:Z",
		"-v", runtimeDir + ":/run/ai-agent:Z",
		"-v", volumeName + ":/home/dev",
		"-e", "AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock",
		imageTag,
	}
	return append(args, command...)
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

func mustRunOutput(t *testing.T, timeout time.Duration, dir string, name string, args ...string) string {
	t.Helper()

	out, err := runOutput(timeout, dir, name, args...)
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func runOutput(timeout time.Duration, dir string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}
