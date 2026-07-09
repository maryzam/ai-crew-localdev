//go:build integration

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func liveRepo(t *testing.T) string {
	t.Helper()
	repo := strings.TrimSpace(os.Getenv("AI_AGENT_LIVE_REPO"))
	if repo == "" {
		t.Skip("set AI_AGENT_LIVE_REPO=owner/repo (an operator-owned scratch repository) to run live tests")
	}
	return repo
}

func liveAgent() string {
	if agent := strings.TrimSpace(os.Getenv("AI_AGENT_LIVE_AGENT")); agent != "" {
		return agent
	}
	return "codex"
}

func liveBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(repoRoot(t), "bin", "ai-agent")
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("bin/ai-agent not built; run make build first: %v", err)
	}
	return binary
}

func ensureLiveBroker(t *testing.T, binary string) {
	t.Helper()
	socketPath := liveBrokerSocketPath()
	if _, err := os.Stat(socketPath); err != nil {
		brokerBinary := filepath.Join(filepath.Dir(binary), "ai-agent-broker")
		broker := exec.Command(brokerBinary)
		broker.Env = append(os.Environ(), "AI_AGENT_BROKER_SOCKET="+socketPath)
		broker.Stdout = os.Stderr
		broker.Stderr = os.Stderr
		if err := broker.Start(); err != nil {
			t.Fatalf("no broker socket at %s and starting %s failed: %v", socketPath, brokerBinary, err)
		}
		t.Cleanup(func() {
			_ = broker.Process.Kill()
			_, _ = broker.Process.Wait()
		})
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(socketPath); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("broker socket %s did not appear after starting the broker", socketPath)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	doctor := exec.Command(binary, "doctor", "--mode", "host")
	out, err := doctor.CombinedOutput()
	if err != nil {
		t.Fatalf("host is not ready for live tests; fix the doctor findings first:\n%s", out)
	}
}

func liveBrokerSocketPath() string {
	if socketPath, ok := os.LookupEnv("AI_AGENT_AUTH_SOCK"); ok {
		if trimmed := strings.TrimSpace(socketPath); trimmed != "" {
			return trimmed
		}
	}
	return paths.DefaultSocketPath()
}

func TestLiveGitHubPushAndPR(t *testing.T) {
	repo := liveRepo(t)
	binary := liveBinary(t)
	ensureLiveBroker(t, binary)

	workDir := t.TempDir()
	clone := exec.Command("git", "clone", "--depth", "1", "https://github.com/"+repo+".git", workDir)
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("clone %s (the scratch repo must be publicly cloneable or credentialed): %v\n%s", repo, err, out)
	}

	branch := fmt.Sprintf("ai-agent-live-%d", time.Now().Unix())
	script := fmt.Sprintf(`set -euo pipefail
cd %q
branch=%q
cleanup() {
  gh pr close "$branch" --delete-branch >/dev/null 2>&1 || true
  git push origin --delete "$branch" >/dev/null 2>&1 || true
}
trap cleanup EXIT
git checkout -q -b "$branch"
date > live-e2e.txt
git add live-e2e.txt
git commit -q -m "live e2e probe"
git push origin "$branch"
gh pr create --title "ai-agent live e2e probe" --body "Automated live E2E probe; closes itself." --head "$branch"
gh pr close "$branch" --delete-branch
`, workDir, branch)

	run := exec.Command(binary, "run", "--agent", liveAgent(), "--repo", workDir, "--", "bash", "-c", script)
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("live push/PR flow failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "ambient") {
		t.Fatalf("unexpected ambient credential mention:\n%s", out)
	}
}

func TestLiveClaudeOAuthReentry(t *testing.T) {
	repo := liveRepo(t)
	binary := liveBinary(t)
	if os.Getenv("AI_AGENT_LIVE_CLAUDE") != "1" {
		t.Skip("set AI_AGENT_LIVE_CLAUDE=1 to validate provider-backed Claude OAuth re-entry")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}
	ensureLiveBroker(t, binary)

	workDir := t.TempDir()
	clone := exec.Command("git", "clone", "--depth", "1", "https://github.com/"+repo+".git", workDir)
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("clone %s: %v\n%s", repo, err, out)
	}

	run := exec.Command(binary, "run", "--agent", "claude", "--repo", workDir, "--",
		"claude", "-p", "Reply with exactly the token LIVE_OK and nothing else.")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("live Claude run failed (is the host claude logged in?): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "LIVE_OK") {
		t.Fatalf("provider-backed Claude request did not complete through persisted OAuth state:\n%s", out)
	}
}
