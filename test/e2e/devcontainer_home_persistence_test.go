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

const claudeAPIKeyHelperSeed = `set -eu
mkdir -p "$HOME/.claude"
cat > "$HOME/.claude/api-key-helper.sh" <<'HELPER'
#!/bin/sh
printf '%s\n' "sk-ant-persisted-offline-key"
HELPER
chmod +x "$HOME/.claude/api-key-helper.sh"
printf '{"apiKeyHelper":"%s/.claude/api-key-helper.sh"}\n' "$HOME" > "$HOME/.claude/settings.json"
`

const claudeOAuthCredentialsSeed = `set -eu
mkdir -p "$HOME/.claude"
cat > "$HOME/.claude/.credentials.json" <<'CREDS'
{"claudeAiOauth":{"accessToken":"persisted-access-token","refreshToken":"persisted-refresh-token","expiresAt":32503680000000,"scopes":["user:inference","user:profile"],"subscriptionType":"max"}}
CREDS
chmod 600 "$HOME/.claude/.credentials.json"
`

func TestLoginPersistsAcrossContainerReplacement(t *testing.T) {
	containerRuntime := newPodmanReadinessRuntime(t)

	imageTag := fmt.Sprintf("ai-agent-login-persistence:%d", time.Now().UnixNano())
	containerRuntime.BuildImage(t, imageTag)
	t.Cleanup(func() {
		containerRuntime.RemoveImage(t, imageTag)
	})

	t.Run("codex-api-key", func(t *testing.T) {
		codexLoginPersistenceCase(t, containerRuntime, imageTag)
	})
	t.Run("claude-api-key-helper", func(t *testing.T) {
		claudeLoginPersistenceCase(t, containerRuntime, imageTag, claudeAPIKeyHelperSeed, "api_key_helper")
	})
	t.Run("claude-oauth-credentials", func(t *testing.T) {
		claudeLoginPersistenceCase(t, containerRuntime, imageTag, claudeOAuthCredentialsSeed, "claude.ai")
	})
}

func codexLoginPersistenceCase(t *testing.T, containerRuntime readinessContainerRuntime, imageTag string) {
	workspaceDir, runtimeDir, homeVolume := loginPersistenceEnv(t, containerRuntime)

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

func claudeLoginPersistenceCase(t *testing.T, containerRuntime readinessContainerRuntime, imageTag string, seedScript string, wantMethod string) {
	workspaceDir, runtimeDir, homeVolume := loginPersistenceEnv(t, containerRuntime)

	seedOut, err := containerRuntime.Run(t, homePersistenceRunSpec(workspaceDir, runtimeDir, homeVolume, imageTag, nil, "sh", "-c", seedScript))
	if err != nil {
		t.Fatalf("seed claude login state: %v\n%s", err, string(seedOut))
	}

	statusOut, err := containerRuntime.Run(t, homePersistenceRunSpec(workspaceDir, runtimeDir, homeVolume, imageTag, nil, "claude", "auth", "status", "--json"))
	if err != nil {
		t.Fatalf("claude auth status failed after container replacement: %v\n%s", err, string(statusOut))
	}
	if !strings.Contains(string(statusOut), `"loggedIn": true`) {
		t.Fatalf("claude did not reuse persisted login state: %s", string(statusOut))
	}
	if !strings.Contains(string(statusOut), fmt.Sprintf(`"authMethod": %q`, wantMethod)) {
		t.Fatalf("claude auth method = %s, want %s", string(statusOut), wantMethod)
	}

	productOut, err := containerRuntime.Run(t, homePersistenceRunSpec(workspaceDir, runtimeDir, homeVolume, imageTag, nil, "ai-agent", "auth", "status"))
	if err != nil {
		t.Fatalf("ai-agent auth status failed after container replacement: %v\n%s", err, string(productOut))
	}
	if !strings.Contains(string(productOut), "[logged_in] claude") {
		t.Fatalf("ai-agent auth status did not report persisted claude login: %s", string(productOut))
	}
}

func loginPersistenceEnv(t *testing.T, containerRuntime readinessContainerRuntime) (workspaceDir string, runtimeDir string, homeVolume string) {
	workspaceDir = filepath.Join(t.TempDir(), "workspace")
	socketRoot, err := os.MkdirTemp("", "aibrk")
	if err != nil {
		t.Fatalf("mkdtemp socket root: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(socketRoot)
	})
	runtimeDir = filepath.Join(socketRoot, "ai-agent")
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

	homeVolume = containerRuntime.CreateVolume(t, fmt.Sprintf("ai-agent-login-%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		containerRuntime.RemoveVolume(t, homeVolume)
	})
	return workspaceDir, runtimeDir, homeVolume
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
