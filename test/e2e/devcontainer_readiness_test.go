//go:build integration

package e2e

import (
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
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/core"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	ghprov "github.com/maryzam/ai-crew-localdev/internal/providers/github"
)

const (
	readinessAgentName  = "claude"
	readinessRepoSlug   = "owner/repo"
	readinessRepoURL    = "https://github.com/owner/repo.git"
	readinessSessionTTL = 30 * time.Minute
	readinessUID        = 1000
	readinessGID        = 1000
)

func TestDevcontainerReadiness(t *testing.T) {
	containerRuntime := newPodmanReadinessRuntime(t)

	h := newReadinessHarness(t, containerRuntime)

	t.Run("managed-session-happy-path", h.managedSessionHappyPath)
	t.Run("missing-broker-socket-fails", h.missingBrokerSocketFails)
}

type readinessHarness struct {
	t                 *testing.T
	containerRuntime  readinessContainerRuntime
	rootDir           string
	repoDir           string
	configDir         string
	runtimeDir        string
	aiAgentRuntimeDir string
	missingRuntimeDir string
	resultsDir        string
	fakeGhDir         string
	imageTag          string
	homeVolume        string
	socketPath        string
	pemPath           string

	cancelBroker context.CancelFunc
}

func newReadinessHarness(t *testing.T, containerRuntime readinessContainerRuntime) *readinessHarness {
	t.Helper()

	rootDir := t.TempDir()
	repoDir := filepath.Join(rootDir, "repo")
	configDir := filepath.Join(rootDir, "config")
	runtimeDir := filepath.Join(rootDir, "runtime")
	aiAgentRuntimeDir := filepath.Join(runtimeDir, "ai-agent")
	missingRuntimeDir := filepath.Join(rootDir, "missing-runtime", "ai-agent")
	resultsDir := filepath.Join(rootDir, "results")
	fakeGhDir := filepath.Join(rootDir, "fake-gh")
	socketPath := filepath.Join(aiAgentRuntimeDir, "broker.sock")

	for _, dir := range []string{repoDir, configDir, runtimeDir, aiAgentRuntimeDir, missingRuntimeDir, resultsDir, fakeGhDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	imageTag := fmt.Sprintf("ai-agent-dev-readiness:%d", time.Now().UnixNano())
	homeVolume := containerRuntime.CreateVolume(t, fmt.Sprintf("ai-agent-readiness-home-%d", time.Now().UnixNano()))

	h := &readinessHarness{
		t:                 t,
		containerRuntime:  containerRuntime,
		rootDir:           rootDir,
		repoDir:           repoDir,
		configDir:         configDir,
		runtimeDir:        runtimeDir,
		aiAgentRuntimeDir: aiAgentRuntimeDir,
		missingRuntimeDir: missingRuntimeDir,
		resultsDir:        resultsDir,
		fakeGhDir:         fakeGhDir,
		imageTag:          imageTag,
		homeVolume:        homeVolume,
		socketPath:        socketPath,
	}

	t.Cleanup(func() {
		if h.cancelBroker != nil {
			h.cancelBroker()
		}
		containerRuntime.RemoveVolume(t, h.homeVolume)
	})

	h.writeBrokerFixtures()
	h.writeFakeGh()
	h.writeContainerScripts()
	h.initRepo()
	h.startBroker()
	h.buildImage()

	t.Cleanup(func() {
		containerRuntime.RemoveImage(t, h.imageTag)
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

	githubSection, err := json.Marshal(map[string]any{
		"installation_id":     42,
		"default_permissions": map[string]string{"contents": "write", "metadata": "read"},
	})
	if err != nil {
		h.t.Fatalf("marshal github section: %v", err)
	}
	pol := policy.PolicyFile{
		SchemaVersion:      "2",
		DefaultSessionTTL:  readinessSessionTTL.String(),
		DefaultIdleTimeout: "5m",
		Agents: map[string]policy.AgentPolicy{
			readinessAgentName: {
				Resources: []string{"github:repo:" + readinessRepoSlug},
				Providers: map[string]json.RawMessage{"github": githubSection},
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
	`, readinessUID, readinessUID, readinessGID, readinessGID, readinessAgentName)
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
if gh auth login > "$results_dir/gh-auth-login.out" 2> "$results_dir/gh-auth-login.err"; then
    echo "expected gh auth login to fail" >&2
    exit 1
fi
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
	audit, err := core.NewFileAuditLogger(auditPath)
	if err != nil {
		h.t.Fatalf("NewFileAuditLogger: %v", err)
	}

	idents := &identity.IdentitiesFile{}
	readJSONFile(h.t, filepath.Join(h.configDir, "identities.json"), idents)

	pol := &policy.PolicyFile{}
	readJSONFile(h.t, filepath.Join(h.configDir, "policy.json"), pol)

	cfg := core.BrokerConfig{
		SocketPath:   h.socketPath,
		AuditLogPath: auditPath,
		SessionTTL:   readinessSessionTTL,
		IdleTimeout:  5 * time.Minute,
	}

	githubProvider := ghprov.New(
		ghprov.NewGitHubClient(ghServer.URL),
		signer,
		func(agent string) string {
			if a, ok := idents.Agents[agent]; ok {
				return a.AppID
			}
			return ""
		},
	)
	br, err := core.NewBroker(
		cfg,
		core.NewPolicyEnforcer(pol, "github"),
		audit,
		[]port.CredentialProvider{githubProvider},
	)
	if err != nil {
		h.t.Fatalf("NewBroker: %v", err)
	}

	ln, err := net.Listen("unix", h.socketPath)
	if err != nil {
		h.t.Fatalf("listen unix socket: %v", err)
	}
	if err := os.Chmod(h.socketPath, 0o600); err != nil {
		_ = ln.Close()
		h.t.Fatalf("chmod socket: %v", err)
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

	h.containerRuntime.BuildImage(h.t, h.imageTag)
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
	blockedAuthErr := readFile(t, filepath.Join(h.resultsDir, "gh-auth-login.err"))
	if !strings.Contains(blockedAuthErr, "do not write personal gh credentials") {
		t.Fatalf("gh auth login was not rejected with actionable guidance:\n%s", blockedAuthErr)
	}
	ghArgs := readFile(t, filepath.Join(h.resultsDir, "gh-args.txt"))
	if strings.Contains(ghArgs, "login") {
		t.Fatalf("blocked gh auth login reached the real gh binary:\n%s", ghArgs)
	}
}

func (h *readinessHarness) missingBrokerSocketFails(t *testing.T) {
	t.Helper()

	t.Log("starting missing-broker-socket negative check")
	out, err := h.runContainerWithRuntime(t, h.missingRuntimeDir)
	if err == nil {
		t.Fatalf("expected missing broker socket run to fail\n%s", string(out))
	}
	if !strings.Contains(string(out), "broker socket") && !strings.Contains(string(out), "not a Unix socket") && !strings.Contains(string(out), "connect to broker") {
		t.Fatalf("unexpected missing-socket failure:\n%s", string(out))
	}
}

func (h *readinessHarness) runContainer(t *testing.T, withSocket bool) ([]byte, error) {
	t.Helper()

	if withSocket {
		return h.runContainerWithRuntime(t, h.aiAgentRuntimeDir)
	}
	return h.runContainerWithRuntime(t, h.missingRuntimeDir)
}

func (h *readinessHarness) runContainerWithRuntime(t *testing.T, runtimeDir string) ([]byte, error) {
	t.Helper()

	return h.containerRuntime.Run(t, readinessRunSpec{
		Workdir: "/workspace/repo",
		Env: []string{
			"AI_AGENT_CONFIG_DIR=/workspace/config",
			"XDG_RUNTIME_DIR=/workspace/runtime",
			"AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock",
			"HOME=" + readinessHomeDir,
			"PATH=/workspace/fake-gh:/usr/local/bin:/usr/bin:/bin",
			"GH_TOKEN=ambient-gh-token",
			"GITHUB_TOKEN=ambient-gh-token",
			fmt.Sprintf("EXPECTED_UID=%d", readinessUID),
			fmt.Sprintf("EXPECTED_GID=%d", readinessGID),
		},
		Mounts: []readinessMount{
			{Source: h.rootDir, Target: "/workspace", Relabel: true},
			{Source: runtimeDir, Target: "/run/ai-agent", Relabel: true},
			{Source: h.homeVolume, Target: readinessHomeDir},
		},
		Image:   h.imageTag,
		Command: []string{"bash", "/workspace/container-check.sh"},
	})
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

func newTestSigner(t *testing.T, pemPath string) *ghprov.Signer {
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
	signer, err := ghprov.NewSigner(idents)
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
