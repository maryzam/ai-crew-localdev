//go:build integration

package modelproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	claudeTokenFileEnv  = "AI_CREW_CLAUDE_TOKEN_FILE"
	claudeVersionsEnv   = "AI_CREW_CLAUDE_VERSIONS"
	pinnedClaudeVersion = "2.1.302"
	compatModel         = "haiku"
	containerHome       = "/claude-home"
	containerWorkspace  = "/workspaces/compat"
)

type fileRecorder struct {
	mu sync.Mutex
	f  *os.File
}

func (r *fileRecorder) Record(ev Event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.f.Write(append(line, '\n')); err != nil {
		return err
	}
	return r.f.Sync()
}

type compatRun struct {
	credential  Credential
	placeholder string
	nonce       string
	socketDir   string
	evidence    string
	listener    net.Listener
	mu          sync.Mutex
	containers  []string
}

func TestClaudeCodeThroughModelProxy(t *testing.T) {
	tokenFile := os.Getenv(claudeTokenFileEnv)
	if tokenFile == "" {
		t.Skipf("%s is not set; the Claude compatibility check needs a host-held setup-token", claudeTokenFileEnv)
	}
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not available")
	}
	credential, err := LoadClaudeSetupToken(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	versions := strings.Fields(os.Getenv(claudeVersionsEnv))
	if len(versions) == 0 {
		versions = []string{pinnedClaudeVersion}
	}
	for _, v := range versions {
		buildClaudeImage(t, v)
	}

	run := startCompatRun(t, credential)

	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			home := run.home(t)
			if got := run.print(t, v, home, "Reply with exactly the word pong"); !strings.Contains(strings.ToLower(got), "pong") {
				t.Fatalf("print mode result %q", got)
			}
			if got := run.print(t, v, home, "Use the Bash tool to run: echo compat-ok > /tmp/f.txt && cat /tmp/f.txt . Then reply with only the file content.", "--allowedTools", "Bash"); !strings.Contains(got, "compat-ok") {
				t.Fatalf("tool loop result %q", got)
			}
			if got := run.print(t, v, home, "What exact text did the file contain? Reply with only that text.", "--continue"); !strings.Contains(got, "compat-ok") {
				t.Fatalf("continued session result %q", got)
			}
			run.interactive(t, v)
		})
	}

	if len(versions) > 1 {
		t.Run("upgrade keeps sessions", func(t *testing.T) {
			home := run.home(t)
			run.print(t, versions[0], home, "Remember the codeword kiwi-42. Reply ok.")
			if got := run.print(t, versions[len(versions)-1], home, "What was the codeword? Reply with only it.", "--continue"); !strings.Contains(got, "kiwi-42") {
				t.Fatalf("upgraded CLI lost the session: %q", got)
			}
		})
	}

	t.Run("placeholder cannot reach the provider", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(`{"model":"claude-haiku-4-5","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+run.placeholder)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		req.Header.Set("Anthropic-Beta", "oauth-2025-04-20")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("provider answered the placeholder with %d", resp.StatusCode)
		}
	})

	t.Run("evidence holds no credential and no refusals", func(t *testing.T) {
		data, err := os.ReadFile(run.evidence)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			var ev Event
			if err := json.Unmarshal(line, &ev); err != nil {
				t.Fatal(err)
			}
			if ev.CredentialEchoed {
				t.Fatalf("provider response echoed the credential: %+v", ev)
			}
			if ev.Decision == Refused && ev.Reason == reasonCredential {
				t.Fatalf("the CLI presented something other than the placeholder: %+v", ev)
			}
		}
	})

	t.Run("broker down stops model access", func(t *testing.T) {
		_ = run.listener.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		defer run.removeContainers()
		out, err := run.agent(ctx, lastVersion(versions), run.home(t), nil, "claude", "-p", "Reply with exactly the word pong", "--model", compatModel, "--output-format", "json").Output()
		if err == nil || strings.Contains(strings.ToLower(resultOf(out)), "pong") {
			t.Fatalf("agent reached the model without the broker: err=%v result=%q", err, resultOf(out))
		}
	})

	assertNoCredentialUnder(t, credential, run.socketDir, filepath.Dir(run.evidence))
}

func buildClaudeImage(t *testing.T, version string) {
	t.Helper()
	cmd := exec.Command("podman", "build", "-q", "--build-arg", "CLAUDE_VERSION="+version, "-t", claudeImage(version), filepath.Join("testdata", "claude"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Claude %s image: %v\n%s", version, err, out)
	}
}

func claudeImage(version string) string {
	return "localhost/ai-crew-modelproxy-claude:" + version
}

func lastVersion(versions []string) string {
	return versions[len(versions)-1]
}

func startCompatRun(t *testing.T, credential Credential) *compatRun {
	t.Helper()
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("", "mp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	evidence := filepath.Join(t.TempDir(), "model-proxy.jsonl")
	f, err := os.OpenFile(evidence, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	run := &compatRun{
		credential:  credential,
		nonce:       hex.EncodeToString(nonce),
		placeholder: "sk-ant-oat01-ai-crew-placeholder-" + hex.EncodeToString(nonce),
		socketDir:   socketDir,
		evidence:    evidence,
	}
	proxy, err := New(Config{
		Upstream:    &url.URL{Scheme: "https", Host: "api.anthropic.com"},
		Placeholder: run.placeholder,
		Credential:  credential,
		Recorder:    &fileRecorder{f: f},
	})
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(socketDir, "model.sock")
	run.listener, err = net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(run.listener) }()
	t.Cleanup(func() { _ = server.Close() })
	t.Cleanup(run.removeContainers)
	return run
}

func (r *compatRun) removeContainers() {
	r.mu.Lock()
	names := append([]string(nil), r.containers...)
	r.mu.Unlock()
	for _, name := range names {
		_ = exec.Command("podman", "rm", "-f", "-t", "0", "--ignore", name).Run()
	}
}

func (r *compatRun) containerName() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := fmt.Sprintf("ai-crew-modelproxy-%s-%d", r.nonce, len(r.containers))
	r.containers = append(r.containers, name)
	return name
}

func (r *compatRun) home(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Cleanup(func() { assertNoCredentialUnder(t, r.credential, home) })
	return home
}

func (r *compatRun) agent(ctx context.Context, version, home string, extraArgs []string, command ...string) *exec.Cmd {
	args := []string{"run", "--rm", "--name", r.containerName(), "--network", "none", "--userns", "keep-id",
		"-v", r.socketDir + ":/run/model",
		"-v", home + ":" + containerHome,
		"-e", "CLAUDE_CONFIG_DIR=" + containerHome,
		"-e", "CLAUDE_CODE_OAUTH_TOKEN=" + r.placeholder,
		"-e", "ANTHROPIC_BASE_URL=http://127.0.0.1:8080",
		"-e", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"-e", "DISABLE_AUTOUPDATER=1",
	}
	args = append(args, extraArgs...)
	args = append(args, claudeImage(version))
	return exec.CommandContext(ctx, "podman", append(args, command...)...)
}

func (r *compatRun) print(t *testing.T, version, home, prompt string, flags ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := append([]string{"claude", "-p", prompt, "--model", compatModel, "--output-format", "json"}, flags...)
	var stderr bytes.Buffer
	cmd := r.agent(ctx, version, home, nil, command...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("claude %s -p failed: %v\n%s", version, err, stderr.Bytes())
	}
	return resultOf(out)
}

func (r *compatRun) interactive(t *testing.T, version string) {
	t.Helper()
	home := r.home(t)
	seed := map[string]any{
		"hasCompletedOnboarding": true,
		"theme":                  "dark",
		"projects":               map[string]any{containerWorkspace: map[string]any{"hasTrustDialogAccepted": true}},
	}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	stdinReader, stdinWriter := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := r.agent(ctx, version, home, []string{"-i", "-e", "TERM=xterm-256color", "-e", "COLUMNS=160", "-e", "LINES=50"},
		"script", "-qfec", "claude --model "+compatModel, containerHome+"/tui.typescript")
	cmd.Stdin = stdinReader
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(home, "tui.typescript")
	steps := []struct {
		wait  time.Duration
		input string
	}{
		{12 * time.Second, "Reply with the word pong immediately followed by the digits 42"},
		{2 * time.Second, "\r"},
	}
	for _, s := range steps {
		time.Sleep(s.wait)
		_, _ = io.WriteString(stdinWriter, s.input)
	}
	time.Sleep(30 * time.Second)
	r.removeContainers()
	_ = stdinWriter.Close()
	_ = cmd.Wait()
	raw, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	screen := regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07|\x1b.`).ReplaceAllString(string(raw), "")
	if !strings.Contains(strings.ToLower(screen), "pong42") {
		t.Fatalf("interactive Claude %s did not answer through the proxy", version)
	}
	for _, prompt := range []string{"Select login method", "API key"} {
		if strings.Contains(screen, prompt) {
			t.Fatalf("interactive Claude %s asked for credentials: %q", version, prompt)
		}
	}
}

func resultOf(out []byte) string {
	var parsed struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return ""
	}
	return parsed.Result
}

func assertNoCredentialUnder(t *testing.T, credential Credential, roots ...string) {
	t.Helper()
	secret := []byte(credential.secret)
	probes := [][]byte{secret, secret[:40], secret[len(secret)-40:]}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.Type().IsRegular() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, probe := range probes {
				if bytes.Contains(data, probe) {
					t.Errorf("credential material found in %s", path)
					return nil
				}
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	}
}
