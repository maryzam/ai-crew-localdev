package login

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
	"github.com/maryzam/ai-crew-localdev/internal/product/securefile"
)

const fakeImageID = "sha256:fake-login-image"

type fakePodman struct {
	log           string
	refreshBinary []byte
}

func TestLoginPublishesVerifiedGenerationForEveryAgent(t *testing.T) {
	for _, name := range agents.Default().Names() {
		t.Run(name, func(t *testing.T) {
			podman := installFakePodman(t)
			t.Setenv("FAKE_IMAGE_MISSING", "1")
			registry := pinnedRegistry(podman.refreshBinary)
			agent, _ := registry.Find(name)
			paths, _ := ResolvePaths(agent)
			published, err := newTestService(registry).Login(context.Background(), name)
			if err != nil {
				t.Fatal(err)
			}
			current, err := Current(registry, name)
			if err != nil || current != published {
				t.Fatalf("current = %#v, %v; published %#v", current, err, published)
			}
			if filepath.Dir(current.Path) != filepath.Join(paths.AgentRoot, current.Generation) {
				t.Fatalf("credential outside its generation: %s", current.Path)
			}
			credential, err := securefile.ReadOwnerOnly(current.Path, agent.CredentialLimit)
			if err != nil {
				t.Fatal(err)
			}
			switch agent.Login {
			case agents.Claude:
				if string(credential) != validClaudeToken+"\n" {
					t.Fatalf("claude credential = %q", credential)
				}
			case agents.Codex:
				if !bytes.Contains(credential, []byte(`"auth_mode":"chatgpt"`)) {
					t.Fatalf("codex credential = %q", credential)
				}
				binary, err := securefile.ReadOwnerOnly(current.RefreshBinary, 1<<10)
				if err != nil || !bytes.Equal(binary, podman.refreshBinary) || filepath.Base(current.RefreshBinary) != "codex" {
					t.Fatalf("refresh binary %s = %q, %v", current.RefreshBinary, binary, err)
				}
			}
			records := custodyRecords(t, paths)
			if decisions(records) != "login_requested,credential_published" {
				t.Fatalf("custody decisions = %s", decisions(records))
			}
			if records[1].CredentialHash != sha256Hex(credential) || records[1].Image != fakeImageID {
				t.Fatalf("publication record = %#v", records[1])
			}
			journal, _ := os.ReadFile(filepath.Join(paths.Evidence, custodyJournal))
			if bytes.Contains(journal, []byte(validClaudeToken)) || bytes.Contains(journal, []byte("refresh-token")) {
				t.Fatalf("custody journal contains a credential: %s", journal)
			}
			podman.assertHardenedRuns(t, paths, agent)
			podman.assertBuiltFromRegistry(t, registry)
		})
	}
}

func TestRepeatedLoginPublishesNewGenerationAndPrunesSuperseded(t *testing.T) {
	podman := installFakePodman(t)
	registry := pinnedRegistry(podman.refreshBinary)
	first := mustLogin(t, registry, "claude")
	second := mustLogin(t, registry, "claude")
	if first.Generation == second.Generation {
		t.Fatal("repeated login reused a generation")
	}
	agent, _ := registry.Find("claude")
	paths, _ := ResolvePaths(agent)
	entries, err := os.ReadDir(paths.AgentRoot)
	if err != nil || len(entries) != 1 || entries[0].Name() != second.Generation {
		t.Fatalf("generations = %v, %v", entries, err)
	}
}

func TestFailedLoginKeepsPublishedGenerationAndRecordsReason(t *testing.T) {
	cases := []struct {
		name   string
		agent  string
		env    map[string]string
		pin    string
		reason string
	}{
		{name: "native login", agent: "codex", env: map[string]string{"FAKE_FAIL": "native"}, reason: "native_login"},
		{name: "rejected token", agent: "claude", env: map[string]string{"FAKE_FAIL": "probe"}, reason: "probe"},
		{name: "oversized output", agent: "claude", env: map[string]string{"FAKE_CLAUDE_OVERFLOW": "1"}, reason: "validation"},
		{name: "incomplete cache", agent: "codex", env: map[string]string{"FAKE_FAIL": "cache"}, reason: "validation"},
		{name: "refresh binary", agent: "codex", pin: strings.Repeat("0", 64), reason: "refresh_binary"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			podman := installFakePodman(t)
			registry := pinnedRegistry(podman.refreshBinary)
			prior := mustLogin(t, registry, test.agent)
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			attempt := registry
			if test.pin != "" {
				attempt = pinnedRegistryHash(test.pin)
			}
			if _, err := newTestService(attempt).Login(context.Background(), test.agent); err == nil {
				t.Fatal("failed login succeeded")
			}
			current, err := Current(registry, test.agent)
			if err != nil || current != prior {
				t.Fatalf("published login changed: %#v, %v", current, err)
			}
			agent, _ := registry.Find(test.agent)
			paths, _ := ResolvePaths(agent)
			records := custodyRecords(t, paths)
			last := records[len(records)-1]
			if last.Decision != decisionFailed || last.Reason != test.reason {
				t.Fatalf("failure record = %#v", last)
			}
		})
	}
}

func TestUnrecordedPublicationIsNotAuthority(t *testing.T) {
	podman := installFakePodman(t)
	registry := pinnedRegistry(podman.refreshBinary)
	prior := mustLogin(t, registry, "codex")
	agent, _ := registry.Find("codex")
	paths, _ := ResolvePaths(agent)
	t.Setenv("FAKE_BREAK_JOURNAL", filepath.Join(paths.Evidence, custodyJournal))
	if _, err := newTestService(registry).Login(context.Background(), "codex"); err == nil {
		t.Fatal("login without a durable publication record succeeded")
	}
	current, err := Current(registry, "codex")
	if err != nil || current != prior {
		t.Fatalf("unrecorded generation became authority: %#v, %v", current, err)
	}
}

func TestRewrittenCredentialIsNotAuthority(t *testing.T) {
	podman := installFakePodman(t)
	registry := pinnedRegistry(podman.refreshBinary)
	published := mustLogin(t, registry, "codex")
	if err := securefile.WriteOwnerOnly(published.Path, []byte(`{"auth_mode":"chatgpt"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Current(registry, "codex"); err == nil || !strings.Contains(err.Error(), "not authoritative") {
		t.Fatalf("rewritten credential error = %v", err)
	}
}

func TestUnpinnedArchitectureRefusesBeforeNativeLogin(t *testing.T) {
	podman := installFakePodman(t)
	registry := pinnedRegistryHash("")
	if _, err := newTestService(registry).Login(context.Background(), "codex"); err == nil || !strings.Contains(err.Error(), "no pinned codex refresh binary") {
		t.Fatalf("unpinned architecture error = %v", err)
	}
	if runs := podman.invocations(t, "run"); len(runs) != 0 {
		t.Fatalf("native login ran without a pinned refresh binary: %v", runs)
	}
}

func TestLoginRefusesBeforePodman(t *testing.T) {
	t.Run("unsupported agent", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		t.Setenv("PATH", t.TempDir())
		if _, err := newTestService(agents.Default()).Login(context.Background(), "other"); err == nil || !strings.Contains(err.Error(), "choose claude or codex") {
			t.Fatalf("unsupported agent error = %v", err)
		}
	})
	t.Run("unsafe evidence", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		t.Setenv("PATH", t.TempDir())
		agent, _ := agents.Default().Find("claude")
		paths, _ := ResolvePaths(agent)
		if err := securefile.EnsureOwnerOnlyDir(paths.Evidence); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(paths.Evidence, custodyJournal), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := newTestService(agents.Default()).Login(context.Background(), "claude"); err == nil || !strings.Contains(err.Error(), "owner-only") {
			t.Fatalf("unsafe evidence error = %v", err)
		}
	})
	t.Run("concurrent login", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		t.Setenv("PATH", t.TempDir())
		agent, _ := agents.Default().Find("claude")
		paths, _ := ResolvePaths(agent)
		if err := preparePaths(paths); err != nil {
			t.Fatal(err)
		}
		held, err := securefile.LockDirectory(paths.LoginRoot, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = held.Close() }()
		if _, err := newTestService(agents.Default()).Login(context.Background(), "codex"); err == nil || !strings.Contains(err.Error(), "already in progress") {
			t.Fatalf("concurrent login error = %v", err)
		}
	})
}

func TestCancelledLoginTerminatesNativeContainer(t *testing.T) {
	podman := installFakePodman(t)
	registry := pinnedRegistry(podman.refreshBinary)
	started := filepath.Join(t.TempDir(), "started")
	t.Setenv("FAKE_HANG", started)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(started); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	if _, err := newTestService(registry).Login(ctx, "codex"); err == nil {
		t.Fatal("cancelled login succeeded")
	}
	if _, err := os.Stat(started + ".terminated"); err != nil || len(podman.invocations(t, "rm")) == 0 {
		t.Fatalf("native container was not terminated and removed: %v, %v", err, podman.invocations(t, "rm"))
	}
}

func newTestService(registry agents.Registry) *Service {
	return New(registry, Console{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Terminal: true})
}

func mustLogin(t *testing.T, registry agents.Registry, name string) Credential {
	t.Helper()
	credential, err := newTestService(registry).Login(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func pinnedRegistry(binary []byte) agents.Registry {
	return pinnedRegistryHash(sha256Hex(binary))
}

func pinnedRegistryHash(hash string) agents.Registry {
	declared := agents.Default().All()
	for index := range declared {
		if declared[index].Refresh != nil {
			declared[index].Refresh.SHA256 = map[string]string{}
			if hash != "" {
				declared[index].Refresh.SHA256[runtime.GOARCH] = hash
			}
		}
	}
	return agents.NewRegistry(declared...)
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func custodyRecords(t *testing.T, paths Paths) []custodyRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(paths.Evidence, custodyJournal))
	if err != nil {
		t.Fatal(err)
	}
	var records []custodyRecord
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var record custodyRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func decisions(records []custodyRecord) string {
	names := make([]string, len(records))
	for index, record := range records {
		names[index] = record.Decision
	}
	return strings.Join(names, ",")
}

func (p fakePodman) invocations(t *testing.T, operation string) [][]string {
	t.Helper()
	data, err := os.ReadFile(p.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var result [][]string
	for _, block := range strings.Split(string(data), "----\n") {
		args := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
		if len(args) > 0 && args[0] == operation {
			result = append(result, args)
		}
	}
	return result
}

func (p fakePodman) assertHardenedRuns(t *testing.T, paths Paths, agent agents.Agent) {
	t.Helper()
	runs := p.invocations(t, "run")
	if len(runs) == 0 {
		t.Fatal("no login container ran")
	}
	for _, args := range runs {
		var volumes []string
		for index, arg := range args {
			if arg == "--volume" {
				volumes = append(volumes, args[index+1])
			}
			if strings.Contains(arg, validClaudeToken) {
				t.Fatalf("credential passed as a podman argument: %v", args)
			}
		}
		if len(volumes) != 1 || !strings.HasPrefix(volumes[0], filepath.Join(paths.LoginRoot, "."+agent.Name+"-staging-")) || !strings.HasSuffix(volumes[0], ":/login:rw,Z") {
			t.Fatalf("login container mounts = %v", volumes)
		}
		joined := strings.Join(args, " ")
		for _, required := range []string{"--pull=never", "--read-only", "--security-opt no-new-privileges", "--cap-drop all", "--userns keep-id", " " + fakeImageID + " "} {
			if !strings.Contains(joined, required) {
				t.Fatalf("login container lacks %q: %v", required, args)
			}
		}
	}
	if agent.Login == agents.Claude && !slices.ContainsFunc(runs, func(args []string) bool { return slices.Contains(args, claudeTokenVariable) }) {
		t.Fatalf("claude token was not verified through the native CLI: %v", runs)
	}
}

func (p fakePodman) assertBuiltFromRegistry(t *testing.T, registry agents.Registry) {
	t.Helper()
	ref, packages := imageRecipe(registry)
	builds := p.invocations(t, "build")
	if len(builds) != 1 || !slices.Contains(builds[0], "AGENT_PACKAGES="+packages) || !slices.Contains(builds[0], ref) {
		t.Fatalf("image builds = %v, want %s from %s", builds, ref, packages)
	}
	if packages != "@anthropic-ai/claude-code@2.1.302 @openai/codex@0.163.0" {
		t.Fatalf("image packages = %s", packages)
	}
}

func installFakePodman(t *testing.T) fakePodman {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	podman := fakePodman{log: filepath.Join(dir, "podman.log"), refreshBinary: []byte("\x7fELF pinned refresh binary")}
	binary := filepath.Join(dir, "refresh-binary")
	if err := os.WriteFile(binary, podman.refreshBinary, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PODMAN_LOG", podman.log)
	t.Setenv("FAKE_REFRESH_BINARY", binary)
	t.Setenv("FAKE_CLAUDE_TOKEN", validClaudeToken)
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" >>"$FAKE_PODMAN_LOG"
printf -- '----\n' >>"$FAKE_PODMAN_LOG"
operation=$1
shift
case "$operation" in
info|create|rm|build) exit 0 ;;
image)
  case "$1" in
  exists) [ -z "${FAKE_IMAGE_MISSING:-}" ] || exit 1; exit 0 ;;
  inspect) printf '` + fakeImageID + `\n'; exit 0 ;;
  esac
  exit 2 ;;
cp) cp "$FAKE_REFRESH_BINARY" "$2"; exit 0 ;;
run) ;;
*) exit 2 ;;
esac
mount=
while [ "$#" -gt 0 ]; do
  case "$1" in
  --volume) shift; mount=$1 ;;
  ` + fakeImageID + `) shift; break ;;
  esac
  shift
done
host=${mount%%:*}
if [ -n "${FAKE_HANG:-}" ]; then
  trap ': >"$FAKE_HANG.terminated"; exit 143' TERM
  : >"$FAKE_HANG"
  while :; do sleep 0.05; done
fi
if [ -n "${FAKE_BREAK_JOURNAL:-}" ]; then
  chmod 400 "$FAKE_BREAK_JOURNAL"
fi
case "$1 ${2:-}" in
"claude setup-token")
  [ "${FAKE_FAIL:-}" != native ] || exit 1
  if [ -n "${FAKE_CLAUDE_OVERFLOW:-}" ]; then
    head -c 70000 /dev/zero | tr '\0' x
  fi
  printf 'Your OAuth token:\r\n\r\n%s\r\n\r\nStore this token securely.\r\n' "$FAKE_CLAUDE_TOKEN"
  exit 0 ;;
"claude -p")
  if [ "${FAKE_FAIL:-}" != probe ] && [ "${CLAUDE_CODE_OAUTH_TOKEN:-}" = "$FAKE_CLAUDE_TOKEN" ]; then
    printf '{"type":"result","is_error":false}\n'
  else
    printf '{"type":"result","is_error":true}\n'
  fi
  exit 0 ;;
codex*)
  [ "${FAKE_FAIL:-}" != native ] || exit 1
  if [ "${FAKE_FAIL:-}" = cache ]; then
    printf '{"auth_mode":"apikey"}\n' >"$host/auth.json"
  else
    printf '%s\n' '{"auth_mode":"chatgpt","tokens":{"access_token":"x.eyJleHAiOjQxMDI0NDQ4MDB9.x","refresh_token":"refresh-token","account_id":"account"}}' >"$host/auth.json"
  fi
  chmod 600 "$host/auth.json"
  exit 0 ;;
esac
exit 2
`
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return podman
}
