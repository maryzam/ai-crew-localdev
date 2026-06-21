//go:build integration

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectDevcontainerE2E(t *testing.T) {
	podmanBin, err := lookPath("podman")
	if err != nil {
		t.Skipf("podman not available: %v", err)
	}
	devcontainerBin, err := lookPath("devcontainer")
	if err != nil {
		t.Skipf("devcontainer CLI not available: %v", err)
	}

	h := newProjectHarness(t)
	toolchainDir := buildHostToolchain(t)
	project := newProjectDevcontainerFixture(t, h.rootDir)
	port := reserveTCPPort(t)
	containerPrefix := fmt.Sprintf("ai-agent-project-e2e-%d", time.Now().UnixNano())

	t.Setenv("AI_AGENT_CONFIG_DIR", h.configDir)
	t.Setenv("XDG_RUNTIME_DIR", h.runtimeDir)
	t.Setenv("AI_AGENT_AUTH_SOCK", h.socketPath)
	t.Setenv("AI_AGENT_PROJECT_E2E_PORT", port)
	t.Setenv("AI_AGENT_PROJECT_E2E_NAME", containerPrefix)
	t.Setenv("PATH", toolchainDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	podmanSocket := startPodmanService(t, podmanBin, h.runtimeDir)
	t.Setenv("DOCKER_HOST", "unix://"+podmanSocket)

	t.Cleanup(func() {
		rmContainer(t, podmanBin, containerPrefix+"-app")
		rmContainer(t, podmanBin, containerPrefix+"-helper")
		rmVolume(t, podmanBin, containerPrefix+"-shared")
		rmProjectContainersByLabel(t, podmanBin, project)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(toolchainDir, "ai-agent"),
		"up", "--project", project, "--runtime", "podman", "--build")
	cmd.Stdin = strings.NewReader("")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("ai-agent up --project failed: %v\n%s", err, out.String())
	}
	upOutput := out.String()
	if !strings.Contains(upOutput, "opening shell in devcontainer") {
		t.Fatalf("project shell entry was not attempted:\n%s", upOutput)
	}

	waitForHTTP(t, "http://127.0.0.1:"+port)
	runProjectValidation(t, devcontainerBin, project, h.runtimeDir)
	assertProjectValidationResults(t, project)
}

func newProjectHarness(t *testing.T) *readinessHarness {
	t.Helper()

	rootDir := t.TempDir()
	configDir := filepath.Join(rootDir, "config")
	runtimeDir := filepath.Join(rootDir, "runtime")
	aiAgentRuntime := filepath.Join(runtimeDir, "ai-agent")
	socketPath := filepath.Join(aiAgentRuntime, "broker.sock")
	for _, dir := range []string{configDir, runtimeDir, aiAgentRuntime} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	h := &readinessHarness{
		t:          t,
		rootDir:    rootDir,
		configDir:  configDir,
		runtimeDir: runtimeDir,
		socketPath: socketPath,
	}
	t.Cleanup(func() {
		if h.cancelBroker != nil {
			h.cancelBroker()
		}
	})

	h.writeBrokerFixtures()
	h.startBroker()
	return h
}

func startPodmanService(t *testing.T, podmanBin string, runtimeDir string) string {
	t.Helper()

	socketDir := filepath.Join(runtimeDir, "podman")
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		t.Fatalf("mkdir podman socket dir: %v", err)
	}
	socketPath := filepath.Join(socketDir, "podman.sock")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, podmanBin, "system", "service", "--time=0", "unix://"+socketPath)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start podman system service: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond); err == nil {
			_ = conn.Close()
			return socketPath
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	t.Fatalf("podman system service did not create %s", socketPath)
	return ""
}

func buildHostToolchain(t *testing.T) string {
	t.Helper()

	root := repoRoot(t)
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir toolchain dir: %v", err)
	}

	builds := []struct {
		out string
		pkg string
	}{
		{"ai-agent", "./cmd/ai-agent"},
		{"ai-agent-broker", "./cmd/ai-agent-broker"},
		{"ai-agent-credential-helper", "./cmd/ai-agent-credential-helper"},
		{"ai-agent-gh", "./cmd/ai-agent-gh"},
	}
	for _, b := range builds {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		cmd := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(binDir, b.out), b.pkg)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("build %s: %v\n%s", b.out, err, string(out))
		}
	}
	return binDir
}

func newProjectDevcontainerFixture(t *testing.T, rootDir string) string {
	t.Helper()

	project := filepath.Join(rootDir, "project")
	devcontainerDir := filepath.Join(project, ".devcontainer")
	scriptsDir := filepath.Join(project, "scripts")
	resultsDir := filepath.Join(project, "results")
	for _, dir := range []string{devcontainerDir, scriptsDir, resultsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	writeProjectFile(t, filepath.Join(devcontainerDir, "devcontainer.json"), `{
  "name": "ai-agent-project-e2e",
  "dockerComposeFile": "compose.yml",
  "service": "app",
  "runServices": ["app", "helper"],
  "workspaceFolder": "/workspace",
  "overrideCommand": false,
  "remoteUser": "root",
  "updateRemoteUserUID": false,
  "postCreateCommand": "project-only-tool post-create > /workspace/results/post-create.txt",
  "forwardPorts": [18080]
}
`)
	writeProjectFile(t, filepath.Join(devcontainerDir, "compose.yml"), `services:
  app:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: "${AI_AGENT_PROJECT_E2E_NAME}-app"
    network_mode: host
    command: "sh -c 'mkdir -p /tmp/project-http && printf ok > /tmp/project-http/index.html && python3 -m http.server ${AI_AGENT_PROJECT_E2E_PORT} --bind 127.0.0.1 --directory /tmp/project-http'"
    environment:
      GH_TOKEN: ambient-personal-token
      GITHUB_TOKEN: ambient-personal-token
      AI_AGENT_PROJECT_E2E_PORT: "${AI_AGENT_PROJECT_E2E_PORT}"
    volumes:
      - ..:/workspace:Z
      - shared:/compose-shared
  helper:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: "${AI_AGENT_PROJECT_E2E_NAME}-helper"
    network_mode: host
    command: "sh -c 'mkdir -p /compose-shared && echo helper-ready > /compose-shared/helper.txt && sleep infinity'"
    volumes:
      - shared:/compose-shared
volumes:
  shared:
    name: "${AI_AGENT_PROJECT_E2E_NAME}-shared"
`)
	writeProjectFile(t, filepath.Join(devcontainerDir, "Dockerfile"), `FROM ubuntu:24.04

USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends git python3 ca-certificates \
    && mkdir -p /usr/local/ai-agent/bin /run/ai-agent \
    && rm -rf /var/lib/apt/lists/*
COPY scripts/project-only-tool /usr/local/bin/project-only-tool
COPY scripts/git-remote-testgit /usr/local/bin/git-remote-testgit
COPY scripts/project-real-gh /usr/local/bin/project-real-gh
RUN chmod +x /usr/local/bin/project-only-tool /usr/local/bin/git-remote-testgit /usr/local/bin/project-real-gh
`)
	writeProjectFile(t, filepath.Join(devcontainerDir, "scripts", "project-only-tool"), `#!/bin/sh
set -eu
printf 'project-tool:%s\n' "${1:-default}"
`)
	writeProjectFile(t, filepath.Join(devcontainerDir, "scripts", "project-real-gh"), `#!/bin/sh
set -eu
mkdir -p /workspace/results
env | sort > /workspace/results/gh-env.txt
printf '%s\n' "$@" > /workspace/results/gh-args.txt
`)
	writeProjectFile(t, filepath.Join(devcontainerDir, "scripts", "git-remote-testgit"), `#!/bin/sh
set -eu
results=/workspace/results
mkdir -p "$results"
printf '%s\n' "$@" > "$results/git-remote-args.txt"
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$results/git-remote-stdin.txt"
  case "$line" in
    capabilities)
      printf 'push\n\n'
      ;;
    list*)
      printf '\n'
      ;;
    push\ *)
      printf 'protocol=https\nhost=github.com\npath=owner/repo.git\n\n' | git credential fill > "$results/git-push-creds.txt"
      env | sort > "$results/git-push-env.txt"
      ref="${line#push }"
      ref="${ref#*:}"
      printf 'ok %s\n\n' "$ref"
      printf '%s\n' "$line" > "$results/git-push.txt"
      ;;
    '')
      ;;
  esac
done
`)
	writeProjectFile(t, filepath.Join(scriptsDir, "validate-project.sh"), `#!/bin/sh
set -eu
results=/workspace/results
mkdir -p "$results"

test -S /run/ai-agent/broker.sock
project-only-tool validate > "$results/project-tool.txt"
cat /compose-shared/helper.txt > "$results/compose-helper.txt"

if touch /run/ai-agent/should-not-write 2> "$results/socket-overlay.err"; then
  echo "socket overlay unexpectedly writable" >&2
  exit 1
fi
if cp /bin/sh /usr/local/ai-agent/bin/ai-agent 2> "$results/toolchain-overlay.err"; then
  echo "toolchain overlay unexpectedly writable" >&2
  exit 1
fi

if GH_TOKEN=ambient-personal-token GITHUB_TOKEN=ambient-personal-token ai-agent-gh auth status > "$results/ambient-gh.out" 2> "$results/ambient-gh.err"; then
  echo "ai-agent-gh accepted ambient credentials outside a managed session" >&2
  exit 1
fi

export GH_TOKEN=ambient-personal-token
export GITHUB_TOKEN=ambient-personal-token
ai-agent run --broker-sock /run/ai-agent/broker.sock --agent claude --repo /workspace -- sh /workspace/scripts/managed-session.sh
`)
	writeProjectFile(t, filepath.Join(scriptsDir, "managed-session.sh"), `#!/bin/sh
set -eu
cd /workspace
git push origin HEAD:main
export AI_AGENT_REAL_GH=/usr/local/bin/project-real-gh
gh auth status --hostname github.com > /workspace/results/gh.out 2> /workspace/results/gh.err
`)

	mustRun(t, project, "git", "init", "-b", "main")
	mustRun(t, project, "git", "config", "user.name", "Project E2E")
	mustRun(t, project, "git", "config", "user.email", "project-e2e@example.com")
	mustRun(t, project, "git", "remote", "add", "origin", readinessRepoURL)
	mustRun(t, project, "git", "remote", "set-url", "--push", "origin", "testgit::owner/repo")
	writeProjectFile(t, filepath.Join(project, "README.md"), "# project e2e\n")
	mustRun(t, project, "git", "add", ".")
	mustRun(t, project, "git", "commit", "-m", "init")
	return project
}

func writeProjectFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func reserveTCPPort(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve tcp port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return fmt.Sprintf("%d", ln.Addr().(*net.TCPAddr).Port)
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			body := make([]byte, 2)
			_, _ = resp.Body.Read(body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			lastErr = fmt.Errorf("status %s", resp.Status)
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("forwarded port %s did not become reachable: %v", url, lastErr)
}

func runProjectValidation(t *testing.T, devcontainerBin string, project string, runtimeDir string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	overlayPath := filepath.Join(runtimeDir, "ai-agent", "devcontainer-broker-overlay-broker.sock.json")
	cmd := exec.CommandContext(ctx, devcontainerBin,
		"exec", "--docker-path", "podman", "--workspace-folder", project,
		"--override-config", overlayPath,
		"--remote-env", "AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock",
		"--remote-env", "PATH=/usr/local/ai-agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"sh", "/workspace/scripts/validate-project.sh")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("project validation failed: %v\n%s", err, out.String())
	}
}

func assertProjectValidationResults(t *testing.T, project string) {
	t.Helper()

	results := filepath.Join(project, "results")
	expectFileContains(t, filepath.Join(results, "post-create.txt"), "project-tool:post-create")
	expectFileContains(t, filepath.Join(results, "project-tool.txt"), "project-tool:validate")
	expectFileContains(t, filepath.Join(results, "compose-helper.txt"), "helper-ready")
	expectFileContains(t, filepath.Join(results, "git-push.txt"), "push HEAD:refs/heads/main")
	expectFileContains(t, filepath.Join(results, "git-push-creds.txt"), "password=ghs_mock_token_123")
	expectFileContains(t, filepath.Join(results, "gh-env.txt"), "GH_TOKEN=ghs_mock_token_123")
	expectFileContains(t, filepath.Join(results, "gh-env.txt"), "GITHUB_TOKEN=ghs_mock_token_123")
	expectFileContains(t, filepath.Join(results, "ambient-gh.err"), "not in a managed session")
	expectFileContains(t, filepath.Join(results, "socket-overlay.err"), "Read-only file system")
	expectFileContains(t, filepath.Join(results, "toolchain-overlay.err"), "Read-only file system")

	for _, path := range []string{
		filepath.Join(results, "git-push-creds.txt"),
		filepath.Join(results, "git-push-env.txt"),
		filepath.Join(results, "gh-env.txt"),
	} {
		if content := readFile(t, path); strings.Contains(content, "ambient-personal-token") {
			t.Fatalf("%s contains ambient personal token:\n%s", path, content)
		}
	}
}

func expectFileContains(t *testing.T, path string, want string) {
	t.Helper()

	content := readFile(t, path)
	if !strings.Contains(content, want) {
		t.Fatalf("%s missing %q:\n%s", path, want, content)
	}
}

func rmContainer(t *testing.T, podmanBin string, name string) {
	t.Helper()
	_, _ = runOutput(time.Minute, "", podmanBin, "rm", "-f", name)
}

func rmVolume(t *testing.T, podmanBin string, name string) {
	t.Helper()
	_, _ = runOutput(time.Minute, "", podmanBin, "volume", "rm", "-f", name)
}

func rmProjectContainersByLabel(t *testing.T, podmanBin string, project string) {
	t.Helper()

	out, err := runOutput(time.Minute, "", podmanBin,
		"ps", "-aq", "--filter", "label=devcontainer.local_folder="+project)
	if err != nil {
		return
	}
	for _, id := range strings.Fields(out) {
		rmContainer(t, podmanBin, id)
	}
}
