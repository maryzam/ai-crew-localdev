//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const journeyImage = "ubuntu:24.04"

func TestCleanHostJourney(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not available")
	}
	root := repoRoot(t)

	dist := buildJourneyArtifact(t, root)
	fixtures := writeJourneyFixtures(t, dist)
	github := startJourneyGitHub(t)
	results := filepath.Join(t.TempDir(), "results")
	if err := os.MkdirAll(results, 0o777); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(
		"podman", "run", "--rm",
		"--network=host",
		"-v", fixtures+":/fixtures:ro,z",
		"-v", results+":/results:rw,z",
		"-e", "AI_AGENT_GITHUB_BASE_URL="+github.URL,
		journeyImage,
		"bash", "/fixtures/journey.sh",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clean-host journey failed: %v\n%s", err, out)
	}

	expectFileContains(t, filepath.Join(results, "version.txt"), "v0.0.0-journey")
	expectFileContains(t, filepath.Join(results, "setup.txt"), `setup complete for agent "codex" (1 repos)`)
	expectFileContains(t, filepath.Join(results, "doctor.txt"), "ready")
	expectFileContains(t, filepath.Join(results, "push-creds.txt"), "password=ghs_journey_token")
	expectFileContains(t, filepath.Join(results, "home-isolation.txt"), "isolated-ok")
	expectFileContains(t, filepath.Join(results, "reentry.txt"), "second-run-ok")
	expectFileContains(t, filepath.Join(results, "runs.txt"), "passed")
	for _, path := range []string{"push-creds.txt", "runs.txt"} {
		if content := readFile(t, filepath.Join(results, path)); strings.Contains(content, "ambient-personal-token") {
			t.Fatalf("%s leaked the planted ambient token:\n%s", path, content)
		}
	}
}

func buildJourneyArtifact(t *testing.T, root string) string {
	t.Helper()
	distDir := t.TempDir()
	artifact := "ai-agent-linux-" + runtime.GOARCH
	build := exec.Command("go", "build", "-trimpath",
		"-ldflags", "-X github.com/maryzam/ai-crew-localdev/internal/cli.Version=v0.0.0-journey",
		"-o", filepath.Join(distDir, artifact), "./cmd/ai-agent")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build journey artifact: %v\n%s", err, out)
	}
	sums := exec.Command("sh", "-c", "sha256sum ai-agent-linux-* > SHA256SUMS")
	sums.Dir = distDir
	if out, err := sums.CombinedOutput(); err != nil {
		t.Fatalf("checksum journey artifact: %v\n%s", err, out)
	}
	return distDir
}

func startJourneyGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/app/installations/42/access_tokens"):
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "ghs_journey_token",
				"expires_at": time.Now().Add(time.Hour).UTC(),
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/installation/repositories"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total_count":  1,
				"repositories": []map[string]any{{"full_name": "journey/repo", "private": true}},
			})
		default:
			t.Errorf("unexpected GitHub API call: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeJourneyFixtures(t *testing.T, dist string) string {
	t.Helper()
	fixtures := t.TempDir()
	releaseDir := filepath.Join(fixtures, "releases", "v0.0.0-journey")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ai-agent-linux-" + runtime.GOARCH, "SHA256SUMS"} {
		data, err := os.ReadFile(filepath.Join(dist, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(releaseDir, name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installer, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtures, "install.sh"), installer, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestPEM(t, filepath.Join(fixtures, "app.pem"))

	journeyFiles := map[string]string{
		"journey.sh": `#!/bin/bash
set -euxo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -q >/dev/null
apt-get install -y -q git ca-certificates >/dev/null
export HOME=/root
export XDG_RUNTIME_DIR=/run/journey
mkdir -p "$XDG_RUNTIME_DIR"
export PATH="$HOME/.local/bin:$PATH"
export GH_TOKEN=ambient-personal-token
export GITHUB_TOKEN=ambient-personal-token

AI_AGENT_RELEASE_BASE_URL=/fixtures/releases AI_AGENT_INSTALL_DIR="$HOME/.local/bin" sh /fixtures/install.sh v0.0.0-journey
ai-agent --version > /results/version.txt

install -m 0755 /fixtures/fake-gh /usr/local/bin/gh
install -m 0755 /fixtures/git-remote-testgit /usr/local/bin/git-remote-testgit

ai-agent setup --non-interactive --agent codex --app-id 42 --pem /fixtures/app.pem --installation-id 42 --repos journey/repo > /results/setup.txt 2>&1
ai-agent-broker &
broker_pid=$!
for _ in $(seq 1 50); do [ -S "$XDG_RUNTIME_DIR/ai-agent/broker.sock" ] && break; sleep 0.2; done
ai-agent doctor --mode host > /results/doctor.txt 2>&1

mkdir -p "$HOME/.ssh" && echo personal-key > "$HOME/.ssh/id_rsa"
git config --global user.name journey
git config --global user.email journey@example.com
mkdir -p /root/work && cd /root/work
git init -q -b main repo && cd repo
git remote add origin https://github.com/journey/repo.git
git remote set-url --push origin testgit::journey/repo
echo journey > README.md
git add . && git commit -q -m init

ai-agent run --agent codex --repo /root/work/repo -- bash /fixtures/agent-session.sh
kill "$broker_pid" && wait "$broker_pid" 2>/dev/null || true
ai-agent-broker &
broker_pid=$!
for _ in $(seq 1 50); do [ -S "$XDG_RUNTIME_DIR/ai-agent/broker.sock" ] && break; sleep 0.2; done
ai-agent run --agent codex --repo /root/work/repo -- sh -c 'echo second-run-ok > /results/reentry.txt'
ai-agent runs list > /results/runs.txt 2>&1
kill "$broker_pid" 2>/dev/null || true
`,
		"agent-session.sh": `#!/bin/bash
set -euo pipefail
test -n "$AI_AGENT_SESSION_ID"
test "$HOME" != "/root"
if [ -e "$HOME/.ssh/id_rsa" ]; then
  echo "personal ssh key reachable in managed run" >&2
  exit 1
fi
echo isolated-ok > /results/home-isolation.txt
cd /root/work/repo
git push origin HEAD:main
`,
		"git-remote-testgit": `#!/bin/sh
set -eu
printf 'protocol=https\nhost=github.com\npath=journey/repo.git\n\n' | git credential fill > /results/push-creds.txt
while read -r line; do
  case "$line" in
  capabilities) printf 'push\n\n' ;;
  list*) printf '\n' ;;
  push*) printf 'ok refs/heads/main\n\n' ;;
  '') exit 0 ;;
  esac
done
`,
		"fake-gh": `#!/bin/sh
echo fake-gh-should-not-run >&2
exit 1
`,
	}
	for name, content := range journeyFiles {
		if err := os.WriteFile(filepath.Join(fixtures, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return fixtures
}
