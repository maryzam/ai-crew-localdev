package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
	"github.com/maryzam/ai-crew-localdev/internal/product/login"
)

const fakeClaudeToken = "sk-ant-oat01-abcdefghijklmnopqrstuvwxyz_0123456789-ABCDEFG"

func TestGreenfieldRootExposesOnlyLandedOperatorCommands(t *testing.T) {
	root := NewRoot(&fakeLoginService{}, &bytes.Buffer{}, &bytes.Buffer{})
	commands := root.Commands()
	if len(commands) != 1 || commands[0].Name() != "login" {
		t.Fatalf("commands = %v", commands)
	}
}

func TestLoginPublishesOnlyValidatedBrokerCredentials(t *testing.T) {
	for _, agent := range agents.All() {
		t.Run(agent.Name, func(t *testing.T) {
			dataHome := t.TempDir()
			t.Setenv("XDG_DATA_HOME", dataHome)
			installFakePodman(t)
			var output bytes.Buffer
			service := login.New(strings.NewReader(""), &output, &output)
			root := NewRoot(service, &output, &output)
			root.SetArgs([]string{"login", agent.Name})
			if err := root.Execute(); err != nil {
				t.Fatalf("login failed: %v\n%s", err, output.String())
			}
			paths, err := login.ResolvePaths(agent)
			if err != nil {
				t.Fatal(err)
			}
			credential, err := os.ReadFile(paths.Credential)
			if err != nil {
				t.Fatal(err)
			}
			if info, err := os.Stat(paths.Credential); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("credential mode: %v, %v", info, err)
			}
			switch agent.Name {
			case "claude":
				if string(credential) != fakeClaudeToken+"\n" {
					t.Fatalf("Claude credential = %q", credential)
				}
			case "codex":
				if !bytes.Contains(credential, []byte(`"auth_mode":"chatgpt"`)) {
					t.Fatalf("Codex credential = %q", credential)
				}
			}
			assertLoginAudit(t, paths.Audit, agent.Name, credential)
		})
	}
}

func TestFailedLoginRetainsPublishedCredentialAndRecordsFailure(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	installFakePodman(t)
	agent, _ := agents.Find("codex")
	paths, err := login.ResolvePaths(agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.AgentRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	prior := []byte("prior-credential")
	if err := os.WriteFile(paths.Credential, prior, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PODMAN_LOGIN_FAIL", "1")
	service := login.New(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	root := NewRoot(service, &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"login", "codex"})
	if err := root.Execute(); err == nil {
		t.Fatal("failed native login succeeded")
	}
	credential, err := os.ReadFile(paths.Credential)
	if err != nil || !bytes.Equal(credential, prior) {
		t.Fatalf("prior credential changed: %q, %v", credential, err)
	}
	audit, err := os.ReadFile(paths.Audit)
	if err != nil || !bytes.Contains(audit, []byte(`"decision":"login_failed"`)) || !bytes.Contains(audit, []byte(`"reason":"native_login"`)) {
		t.Fatalf("failure audit = %q, %v", audit, err)
	}
}

func TestLoginRefusesUnsupportedAgentBeforePodman(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	service := login.New(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	root := NewRoot(service, &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"login", "other"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "choose claude or codex") {
		t.Fatalf("unsupported agent error = %v", err)
	}
}

func TestLoginRefusesBeforePodmanWhenAuditIsUnsafe(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	agent, _ := agents.Find("claude")
	paths, err := login.ResolvePaths(agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.LoginRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Audit, []byte("unsafe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := login.New(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	root := NewRoot(service, &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"login", "claude"})
	err = root.Execute()
	if err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("unsafe audit error = %v", err)
	}
	if _, err := os.Stat(paths.Credential); !os.IsNotExist(err) {
		t.Fatalf("credential exists after audit refusal: %v", err)
	}
}

func assertLoginAudit(t *testing.T, path, agent string, credential []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("audit lines = %d: %s", len(lines), data)
	}
	want := []string{"login_requested", "credential_validated", "credential_published"}
	digest := sha256.Sum256(credential)
	wantHash := fmt.Sprintf("%x", digest)
	for index, line := range lines {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if event["agent"] != agent || event["decision"] != want[index] {
			t.Fatalf("audit event %d = %v", index, event)
		}
		if index == 0 && event["credential_sha256"] != nil {
			t.Fatalf("request event contains credential hash: %v", event)
		}
		if index > 0 && event["credential_sha256"] != wantHash {
			t.Fatalf("credential hash = %v, want %s", event["credential_sha256"], wantHash)
		}
		if strings.Contains(string(line), fakeClaudeToken) || strings.Contains(string(line), "refresh-token") {
			t.Fatalf("audit contains a credential: %s", line)
		}
	}
}

func installFakePodman(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "podman")
	script := `#!/bin/sh
set -eu
operation=$1
shift
if [ "$operation" = "info" ]; then
  printf '{}\n'
  exit 0
fi
if [ "$operation" = "image" ] && [ "$1" = "exists" ]; then
  exit 0
fi
if [ "$operation" = "image" ] && [ "$1" = "inspect" ]; then
  printf 'sha256:fake-login-image\n'
  exit 0
fi
if [ "$operation" = "rm" ]; then
  exit 0
fi
if [ "$operation" = "create" ]; then
  printf 'fake-container\n'
  exit 0
fi
if [ "$operation" = "cp" ]; then
  destination=$2
  cp /bin/true "$destination"
  chmod 700 "$destination"
  exit 0
fi
if [ "$operation" != "run" ]; then
  exit 2
fi
mount=
agent=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--volume" ]; then
    shift
    mount=$1
  fi
  if [ "$1" = "claude" ] || [ "$1" = "codex" ]; then
    agent=$1
  fi
  shift
done
if [ "${FAKE_PODMAN_LOGIN_FAIL:-}" = "1" ]; then
  exit 1
fi
host=${mount%%:*}
if [ "$agent" = "claude" ]; then
  printf 'Setup complete\n` + fakeClaudeToken + `\n'
  exit 0
fi
if [ "$agent" = "codex" ]; then
  printf '%s\n' '{"auth_mode":"chatgpt","tokens":{"access_token":"x.eyJleHAiOjQxMDI0NDQ4MDB9.x","refresh_token":"refresh-token","account_id":"account"}}' >"$host/auth.json"
  chmod 600 "$host/auth.json"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type fakeLoginService struct{}

func (*fakeLoginService) Login(_ context.Context, agent string) (login.Result, error) {
	return login.Result{Agent: agent}, nil
}
