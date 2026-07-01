package launcher

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestDevcontainerEntrypointMissingAuthSockEnv(t *testing.T) {
	result := runDevcontainerEntrypoint(t, map[string]*string{
		"AI_AGENT_AUTH_SOCK": nil,
	}, "/bin/true")

	if result.err == nil {
		t.Fatal("expected entrypoint to fail when AI_AGENT_AUTH_SOCK is missing")
	}
	assertOutputContains(t, result.stderr, "AI_AGENT_AUTH_SOCK is not set")
	assertOutputContains(t, result.stderr, "broker socket at /run/ai-agent/broker.sock")
}

func TestDevcontainerEntrypointMissingSocket(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "broker.sock")
	result := runDevcontainerEntrypoint(t, map[string]*string{
		"AI_AGENT_AUTH_SOCK": strPtr(sockPath),
	}, "/bin/true")

	if result.err == nil {
		t.Fatal("expected entrypoint to fail when the broker socket is missing")
	}
	assertOutputContains(t, result.stderr, sockPath)
	assertOutputContains(t, result.stderr, "broker socket not found")
	assertOutputContains(t, result.stderr, "ai-agent-broker.socket")
}

func TestDevcontainerEntrypointWrongFileType(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "broker.sock")
	if err := os.WriteFile(sockPath, []byte("not-a-socket"), 0600); err != nil {
		t.Fatalf("write regular file: %v", err)
	}

	result := runDevcontainerEntrypoint(t, map[string]*string{
		"AI_AGENT_AUTH_SOCK": strPtr(sockPath),
	}, "/bin/true")

	if result.err == nil {
		t.Fatal("expected entrypoint to fail when the mounted path is not a socket")
	}
	assertOutputContains(t, result.stderr, sockPath)
	assertOutputContains(t, result.stderr, "expected a Unix socket")
	assertOutputContains(t, result.stderr, "regular file")
}

func TestDevcontainerEntrypointPermissionDenied(t *testing.T) {
	sockPath, cleanup := listenUnixSocket(t)
	defer cleanup()

	if err := os.Chmod(sockPath, 0); err != nil {
		t.Fatalf("chmod socket: %v", err)
	}

	result := runDevcontainerEntrypoint(t, map[string]*string{
		"AI_AGENT_AUTH_SOCK": strPtr(sockPath),
	}, "/bin/true")

	if result.err == nil {
		t.Fatal("expected entrypoint to fail when the socket is not writable")
	}
	assertOutputContains(t, result.stderr, sockPath)
	assertOutputContains(t, result.stderr, "is not accessible to uid")
}

func TestDevcontainerEntrypointHealthyStartup(t *testing.T) {
	sockPath, cleanup := listenUnixSocket(t)
	defer cleanup()

	result := runDevcontainerEntrypoint(t, map[string]*string{
		"AI_AGENT_AUTH_SOCK": strPtr(sockPath),
	}, "/bin/sh", "-c", "printf healthy")

	if result.err != nil {
		t.Fatalf("entrypoint failed unexpectedly: %v\nstderr: %s", result.err, result.stderr)
	}
	if got := result.stdout; got != "healthy" {
		t.Fatalf("stdout = %q, want %q", got, "healthy")
	}
	if strings.TrimSpace(result.stderr) != "" {
		t.Fatalf("stderr = %q, want empty", result.stderr)
	}
}

type entrypointResult struct {
	stdout string
	stderr string
	err    error
}

func runDevcontainerEntrypoint(t *testing.T, env map[string]*string, args ...string) entrypointResult {
	t.Helper()

	if _, ok := env["AI_AGENT_WORKSPACE_DIR"]; !ok {
		env["AI_AGENT_WORKSPACE_DIR"] = strPtr(t.TempDir())
	}
	stubDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubDir, "ai-agent"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env["PATH"] = strPtr(stubDir + string(os.PathListSeparator) + os.Getenv("PATH"))
	env["HOME"] = strPtr(t.TempDir())

	script := filepath.Join(repoRoot(t), ".devcontainer", "entrypoint.sh")
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = mergeEnv(env)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return entrypointResult{
		stdout: strings.TrimSuffix(stdout.String(), "\n"),
		stderr: stderr.String(),
		err:    err,
	}
}

func listenUnixSocket(t *testing.T) (string, func()) {
	t.Helper()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "broker.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}

	if err := os.Chmod(sockPath, 0o600); err != nil {
		t.Fatalf("chmod socket: %v", err)
	}

	return sockPath, func() {
		_ = ln.Close()
	}
}

func mergeEnv(overrides map[string]*string) []string {
	base := os.Environ()
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	scrub := make(map[string]struct{}, len(overrides))
	for _, key := range keys {
		scrub[key] = struct{}{}
	}

	result := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		key, _, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		if _, ok := scrub[key]; ok {
			continue
		}
		result = append(result, kv)
	}

	for _, key := range keys {
		if value := overrides[key]; value != nil {
			result = append(result, fmt.Sprintf("%s=%s", key, *value))
		}
	}

	return result
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

func strPtr(s string) *string {
	return &s
}

func assertOutputContains(t *testing.T, output, substr string) {
	t.Helper()

	if !strings.Contains(output, substr) {
		t.Fatalf("output %q does not contain %q", output, substr)
	}
}
