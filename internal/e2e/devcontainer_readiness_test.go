//go:build integration

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

const (
	readinessAgentName  = "claude"
	readinessRepoSlug   = "owner/repo"
	readinessRepoURL    = "https://github.com/owner/repo.git"
	readinessSessionTTL = 30 * time.Minute
)

func TestDevcontainerReadiness(t *testing.T) {
	dockerBin, err := lookPath("docker")
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}

	h := newReadinessHarness(t, dockerBin)

	t.Run("managed-session-happy-path", h.managedSessionHappyPath)
	t.Run("missing-broker-socket-fails", h.missingBrokerSocketFails)
}

type readinessHarness struct {
	t          *testing.T
	dockerBin  string
	rootDir    string
	repoDir    string
	configDir  string
	runtimeDir string
	resultsDir string
	fakeGhDir  string
	hostUID    int
	hostGID    int
	imageTag   string
	socketPath string
	pemPath    string

	cancelBroker context.CancelFunc
}

func newReadinessHarness(t *testing.T, dockerBin string) *readinessHarness {
	t.Helper()

	rootDir := t.TempDir()
	repoDir := filepath.Join(rootDir, "repo")
	configDir := filepath.Join(rootDir, "config")
	runtimeDir := filepath.Join(rootDir, "runtime")
	resultsDir := filepath.Join(rootDir, "results")
	fakeGhDir := filepath.Join(rootDir, "fake-gh")
	aiAgentRuntime := filepath.Join(runtimeDir, "ai-agent")
	socketPath := filepath.Join(aiAgentRuntime, "broker.sock")

	for _, dir := range []string{repoDir, configDir, runtimeDir, aiAgentRuntime, resultsDir, fakeGhDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	hostUID := os.Getuid()
	hostGID := os.Getgid()
	imageTag := fmt.Sprintf("ai-agent-dev-readiness:%d", time.Now().UnixNano())

	h := &readinessHarness{
		t:          t,
		dockerBin:  dockerBin,
		rootDir:    rootDir,
		repoDir:    repoDir,
		configDir:  configDir,
		runtimeDir: runtimeDir,
		resultsDir: resultsDir,
		fakeGhDir:  fakeGhDir,
		hostUID:    hostUID,
		hostGID:    hostGID,
		imageTag:   imageTag,
		socketPath: socketPath,
	}

	t.Cleanup(func() {
		if h.cancelBroker != nil {
			h.cancelBroker()
		}
	})

	h.writeBrokerFixtures()
	h.writeFakeGh()
	h.writeContainerScripts()
	h.initRepo()
	h.startBroker()
	h.buildImage()

	t.Cleanup(func() {
		// Remove the test image to avoid accumulating dangling images.
		_ = exec.Command(dockerBin, "rmi", "-f", h.imageTag).Run()
	})

	return h
}

func (h *readinessHarness) writeBrokerFixtures() {
	h.t.Helper()

	h.pemPath = writeTestPEM(h.t, filepath.Join(h.rootDir, "test-agent.pem"))

	idents := identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents: map[string]identity.AgentIdentity{
			readinessAgentName: {
				AppID:      "12345",
				AppKey:     h.pemPath,
				GitName:    "claude[bot]",
				GitEmail:   "claude@bot",
				GithubHost: "github.com",
				Tool:       "claude-code",
				Model:      "claude-sonnet-4-6",
			},
		},
	}
	writeJSONFile(h.t, filepath.Join(h.configDir, "identities.json"), idents)

	installID := int64(42)
	pol := policy.PolicyFile{
		SchemaVersion:      "ai-agent-policy/v1",
		DefaultSessionTTL:  readinessSessionTTL.String(),
		DefaultIdleTimeout: "5m",
		Agents: map[string]policy.AgentPolicy{
			readinessAgentName: {
				AllowedRepos:       []string{readinessRepoSlug},
				InstallationID:     &installID,
				DefaultPermissions: map[string]string{"contents": "write", "metadata": "read"},
			},
		},
	}
	writeJSONFile(h.t, filepath.Join(h.configDir, "policy.json"), pol)
}

func (h *readinessHarness) writeFakeGh() {
	h.t.Helper()

	ghPath := filepath.Join(h.fakeGhDir, "gh")
	script := `#!/bin/sh
set -eu
output_dir="/workspace/results"
mkdir -p "$output_dir"
env | sort > "$output_dir/gh-env.txt"
printf '%s\n' "$@" > "$output_dir/gh-args.txt"
exit 0
`
	if err := os.WriteFile(ghPath, []byte(script), 0o755); err != nil {
		h.t.Fatalf("write fake gh: %v", err)
	}
}

func (h *readinessHarness) writeContainerScripts() {
	h.t.Helper()

	outerPath := filepath.Join(h.rootDir, "container-check.sh")
	innerPath := filepath.Join(h.rootDir, "session-check.sh")

	outerScript := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

if [[ ! -S /run/ai-agent/broker.sock ]]; then
    echo "missing broker socket at /run/ai-agent/broker.sock" >&2
    exit 1
fi
if [[ "$(id -u)" != "%d" ]]; then
    echo "unexpected uid: $(id -u), want %d" >&2
    exit 1
fi
if [[ "$(id -g)" != "%d" ]]; then
    echo "unexpected gid: $(id -g), want %d" >&2
    exit 1
fi
test -d /workspace/repo
test -w /workspace/results

export PATH="/workspace/fake-gh:$PATH"
export GH_TOKEN="ambient-gh-token"
export GITHUB_TOKEN="ambient-gh-token"

cd /workspace/repo
ai-agent run --broker-sock /run/ai-agent/broker.sock --agent %s --repo . -- bash /workspace/session-check.sh
`, h.hostUID, h.hostUID, h.hostGID, h.hostGID, readinessAgentName)
	if err := os.WriteFile(outerPath, []byte(outerScript), 0o755); err != nil {
		h.t.Fatalf("write outer script: %v", err)
	}

	innerScript := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

repo_dir="/workspace/repo"
results_dir="/workspace/results"

printf 'protocol=https\nhost=github.com\npath=%%s.git\n\n' '%s' | git -C "$repo_dir" credential fill > "$results_dir/git-creds.txt"

if printf 'protocol=https\nhost=github.com\npath=%%s.git\n\n' '%s' | env -u AI_AGENT_SESSION_BIND_FD git -C "$repo_dir" credential fill > "$results_dir/missing-bind.out" 2> "$results_dir/missing-bind.err"; then
    echo "expected missing AI_AGENT_SESSION_BIND_FD to fail" >&2
    exit 1
fi
export AI_AGENT_REAL_GH=/workspace/fake-gh/gh
gh auth status --hostname github.com > "$results_dir/gh.out" 2> "$results_dir/gh.err"
`, readinessRepoSlug, readinessRepoSlug)
	if err := os.WriteFile(innerPath, []byte(innerScript), 0o755); err != nil {
		h.t.Fatalf("write inner script: %v", err)
	}
}

func (h *readinessHarness) initRepo() {
	h.t.Helper()

	mustRun(h.t, h.repoDir, "git", "init", "-b", "main")
	mustRun(h.t, h.repoDir, "git", "config", "user.name", "Readiness Test")
	mustRun(h.t, h.repoDir, "git", "config", "user.email", "readiness@example.com")
	mustRun(h.t, h.repoDir, "git", "remote", "add", "origin", readinessRepoURL)
	if err := os.WriteFile(filepath.Join(h.repoDir, "README.md"), []byte("# readiness\n"), 0o644); err != nil {
		h.t.Fatalf("write repo file: %v", err)
	}
	mustRun(h.t, h.repoDir, "git", "add", "README.md")
	mustRun(h.t, h.repoDir, "git", "commit", "-m", "init")
}

func (h *readinessHarness) startBroker() {
	h.t.Helper()

	ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			h.t.Fatalf("unexpected method: %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/app/installations/42/access_tokens") {
			h.t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_mock_token_123",
			"expires_at": time.Now().Add(time.Hour).UTC(),
		})
	}))

	signer := newTestSigner(h.t, h.pemPath)

	auditPath := filepath.Join(h.rootDir, "audit.log")
	audit, err := broker.NewFileAuditLogger(auditPath)
	if err != nil {
		h.t.Fatalf("NewFileAuditLogger: %v", err)
	}

	idents := &identity.IdentitiesFile{}
	readJSONFile(h.t, filepath.Join(h.configDir, "identities.json"), idents)

	pol := &policy.PolicyFile{}
	readJSONFile(h.t, filepath.Join(h.configDir, "policy.json"), pol)

	cfg := broker.BrokerConfig{
		SocketPath:    h.socketPath,
		AuditLogPath:  auditPath,
		GitHubBaseURL: ghServer.URL,
		SessionTTL:    readinessSessionTTL,
		IdleTimeout:   5 * time.Minute,
	}

	br := broker.NewBroker(cfg, idents, broker.NewPolicyEnforcer(pol), signer, audit)

	ln, err := net.Listen("unix", h.socketPath)
	if err != nil {
		h.t.Fatalf("listen unix socket: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.cancelBroker = func() {
		cancel()
		_ = ln.Close()
		audit.Close()
		ghServer.Close()
	}
	go func() {
		_ = br.Serve(ctx, ln)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			h.t.Fatalf("broker socket %s did not become ready", h.socketPath)
		}
		if _, err := os.Stat(h.socketPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := net.DialTimeout("unix", h.socketPath, time.Second); err != nil {
		h.t.Fatalf("dial broker socket: %v", err)
	}

}

func (h *readinessHarness) buildImage() {
	h.t.Helper()

	if image := os.Getenv("AI_AGENT_READINESS_IMAGE"); image != "" {
		h.imageTag = image
		return
	}

	root := repoRoot(h.t)
	h.t.Logf("building readiness image %s", h.imageTag)
	cmd := exec.CommandContext(context.Background(), h.dockerBin,
		"build", "--progress=plain", "-f", ".devcontainer/Dockerfile", "-t", h.imageTag, ".")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("docker build failed: %v\n%s", err, string(out))
	}
	h.t.Logf("built readiness image %s", h.imageTag)
}

func (h *readinessHarness) managedSessionHappyPath(t *testing.T) {
	t.Helper()

	t.Log("starting managed-session happy path")
	out, err := h.runContainer(t, true)
	if err != nil {
		t.Fatalf("container run failed: %v\n%s", err, string(out))
	}
	t.Logf("container output:\n%s", string(out))

	gitCreds := readFile(t, filepath.Join(h.resultsDir, "git-creds.txt"))
	if !strings.Contains(gitCreds, "password=ghs_mock_token_123") {
		t.Fatalf("git credential helper did not return broker token:\n%s", gitCreds)
	}

	missingBindErr := readFile(t, filepath.Join(h.resultsDir, "missing-bind.err"))
	if !strings.Contains(missingBindErr, "AI_AGENT_SESSION_BIND_FD not set") {
		t.Fatalf("missing bind FD failure was not deterministic:\n%s", missingBindErr)
	}

	ghEnv := readFile(t, filepath.Join(h.resultsDir, "gh-env.txt"))
	if !strings.Contains(ghEnv, "GH_TOKEN=ghs_mock_token_123") {
		t.Fatalf("gh wrapper did not receive broker token:\n%s", ghEnv)
	}
	if !strings.Contains(ghEnv, "GITHUB_TOKEN=ghs_mock_token_123") {
		t.Fatalf("gh wrapper did not receive broker token in GITHUB_TOKEN:\n%s", ghEnv)
	}
	if strings.Contains(ghEnv, "ambient-gh-token") {
		t.Fatalf("ambient gh credentials were not scrubbed:\n%s", ghEnv)
	}
	if !strings.Contains(ghEnv, "AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock") {
		t.Fatalf("gh wrapper did not preserve broker socket env:\n%s", ghEnv)
	}
}

func (h *readinessHarness) missingBrokerSocketFails(t *testing.T) {
	t.Helper()

	t.Log("starting missing-broker-socket negative check")
	cmd := []string{
		"-w", "/workspace/repo",
		"-e", "AI_AGENT_CONFIG_DIR=/workspace/config",
		"-e", "XDG_RUNTIME_DIR=/workspace/runtime",
		"-e", "AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock",
		"-e", "PATH=/workspace/fake-gh:/usr/local/bin:/usr/bin:/bin",
		"-e", "GH_TOKEN=ambient-gh-token",
		"-e", "GITHUB_TOKEN=ambient-gh-token",
		"-e", fmt.Sprintf("EXPECTED_UID=%d", h.hostUID),
		"-e", fmt.Sprintf("EXPECTED_GID=%d", h.hostGID),
		"-v", h.rootDir + ":/workspace",
		"--user", strconv.Itoa(h.hostUID) + ":" + strconv.Itoa(h.hostGID),
		h.imageTag,
		"bash", "/workspace/container-check.sh",
	}
	out, err := h.runDocker(t, append([]string{"run", "--rm"}, cmd...)...)
	if err == nil {
		t.Fatalf("expected missing broker socket run to fail\n%s", string(out))
	}
	if !strings.Contains(string(out), "broker socket") && !strings.Contains(string(out), "not a Unix socket") && !strings.Contains(string(out), "connect to broker") {
		t.Fatalf("unexpected missing-socket failure:\n%s", string(out))
	}
}

func (h *readinessHarness) runContainer(t *testing.T, withSocket bool) ([]byte, error) {
	t.Helper()

	args := []string{
		"run", "--rm",
		"-w", "/workspace/repo",
		"-e", "AI_AGENT_CONFIG_DIR=/workspace/config",
		"-e", "XDG_RUNTIME_DIR=/workspace/runtime",
		"-e", "AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock",
		"-e", "PATH=/workspace/fake-gh:/usr/local/bin:/usr/bin:/bin",
		"-e", "GH_TOKEN=ambient-gh-token",
		"-e", "GITHUB_TOKEN=ambient-gh-token",
		"-v", h.rootDir + ":/workspace",
		"--user", strconv.Itoa(h.hostUID) + ":" + strconv.Itoa(h.hostGID),
	}
	if withSocket {
		args = append(args, "-v", h.socketPath+":/run/ai-agent/broker.sock:ro")
	}
	args = append(args, h.imageTag, "bash", "/workspace/container-check.sh")
	return h.runDocker(t, args...)
}

func (h *readinessHarness) runDocker(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.dockerBin, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.Bytes(), err
}

func lookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, string(out))
	}
}

func writeTestPEM(t *testing.T, path string) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := os.WriteFile(path, pemData, 0o600); err != nil {
		t.Fatalf("write PEM: %v", err)
	}
	return path
}

func newTestSigner(t *testing.T, pemPath string) *broker.Signer {
	t.Helper()

	idents := &identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents: map[string]identity.AgentIdentity{
			readinessAgentName: {
				AppID:      "12345",
				AppKey:     pemPath,
				GitName:    "claude[bot]",
				GitEmail:   "claude@bot",
				GithubHost: "github.com",
				Tool:       "claude-code",
				Model:      "claude-sonnet-4-6",
			},
		},
	}
	signer, err := broker.NewSigner(idents)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return signer
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readJSONFile(t *testing.T, path string, dst any) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
