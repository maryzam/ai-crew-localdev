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
	"strings"
	"testing"
	"time"
)

const (
	codexAuthFileEnv   = "AI_CREW_CODEX_AUTH_FILE"
	codexVersionsEnv   = "AI_CREW_CODEX_VERSIONS"
	codexBinaryHashEnv = "AI_CREW_CODEX_BINARY_SHA256"
	pinnedCodexVersion = "0.163.0"
)

func TestCodexThroughModelProxy(t *testing.T) {
	authPath := os.Getenv(codexAuthFileEnv)
	if authPath == "" {
		t.Skipf("%s is not set; the Codex compatibility check needs a dedicated broker login", codexAuthFileEnv)
	}
	binaryHash := os.Getenv(codexBinaryHashEnv)
	if binaryHash == "" {
		t.Skipf("%s is not set; the refresh binary must be pinned", codexBinaryHashEnv)
	}
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not available")
	}
	codexBinary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	codexBinary, err = filepath.Abs(codexBinary)
	if err != nil {
		t.Fatal(err)
	}
	versions := strings.Fields(os.Getenv(codexVersionsEnv))
	if len(versions) == 0 {
		versions = []string{pinnedCodexVersion}
	}
	for _, version := range versions {
		buildCodexImage(t, version)
	}
	before := codexAuthSecrets(t, authPath)
	dir, err := os.MkdirTemp("", "codex-model-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(dir, "evidence.jsonl")
	file, err := os.OpenFile(evidence, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	recorder := &fileRecorder{f: file}
	binaryDir := filepath.Join(dir, "refresh-binary")
	if err := os.Mkdir(binaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pinnedBinary := filepath.Join(binaryDir, "codex")
	input, err := os.Open(codexBinary)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(pinnedBinary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	_ = input.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal("cannot install pinned broker refresh binary")
	}
	source, err := NewCodexAuthSource(authPath, pinnedBinary, binaryHash, recorder)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	placeholder := "ai-crew-codex-" + hex.EncodeToString(nonce)
	proxy, err := NewCodex(CodexConfig{
		Upstream:    &url.URL{Scheme: "https", Host: "chatgpt.com"},
		Placeholder: placeholder,
		Auth:        source,
		Recorder:    recorder,
	})
	if err != nil {
		t.Fatal(err)
	}
	socketDir := filepath.Join(dir, "socket")
	if err := os.Mkdir(socketDir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(socketDir, "model.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	home := t.TempDir()
	config := strings.Join([]string{
		"model = \"gpt-6-sol\"",
		"model_provider = \"ai_crew_proxy\"",
		"[model_providers.ai_crew_proxy]",
		"name = \"AI Crew Codex Proxy\"",
		"base_url = \"http://127.0.0.1:8080/backend-api/codex\"",
		"wire_api = \"responses\"",
		"env_key = \"AI_CREW_PLACEHOLDER\"",
		"requires_openai_auth = false",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context, version, agentHome, work string, command ...string) error {
		args := []string{"run", "--rm", "--network", "none", "--userns", "keep-id",
			"-v", socketDir + ":/run/model:ro", "-v", agentHome + ":/codex-home", "-v", work + ":/workspaces/compat",
			"-e", "CODEX_HOME=/codex-home", "-e", "AI_CREW_PLACEHOLDER=" + placeholder, codexImage(version)}
		cmd := exec.CommandContext(ctx, "podman", append(args, command...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("Codex %s exited: %w (stderr bytes %d)", version, err, stderr.Len())
		}
		return nil
	}
	source.Invalidate(string(before[0]))
	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			work := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := run(ctx, version, home, work, "codex", "exec", "--skip-git-repo-check", "--sandbox", "read-only", "--color", "never", "-C", "/workspaces/compat", "-o", "/codex-home/last-message.txt", "Reply exactly AI_CREW_CODEX_OK and do not use tools."); err != nil {
				t.Fatal(err)
			}
			message, err := os.ReadFile(filepath.Join(home, "last-message.txt"))
			if err != nil || strings.TrimSpace(string(message)) != "AI_CREW_CODEX_OK" {
				t.Fatalf("Codex %s did not return the expected answer", version)
			}
			if err := run(ctx, version, home, work, "codex", "exec", "--skip-git-repo-check", "--sandbox", "workspace-write", "--color", "never", "-C", "/workspaces/compat", "-o", "/codex-home/tool-message.txt", "Use the shell tool to write exactly AI_CREW_TOOL_OK into tool-proof.txt, then reply exactly AI_CREW_TOOL_OK."); err != nil {
				t.Fatal(err)
			}
			proof, err := os.ReadFile(filepath.Join(work, "tool-proof.txt"))
			if err != nil || strings.TrimSpace(string(proof)) != "AI_CREW_TOOL_OK" {
				t.Fatal("Codex did not complete the tool loop")
			}
			if err := run(ctx, version, home, work, "codex", "exec", "--skip-git-repo-check", "resume", "--last", "-o", "/codex-home/resume-message.txt", "What exact text did your previous shell tool write? Reply with only that text, without using tools."); err != nil {
				t.Fatal(err)
			}
			resumed, err := os.ReadFile(filepath.Join(home, "resume-message.txt"))
			if err != nil || strings.TrimSpace(string(resumed)) != "AI_CREW_TOOL_OK" {
				t.Fatal("Codex did not resume the prior session")
			}
			assertSecretsAbsent(t, append(before, codexAuthSecrets(t, authPath)...), home, work)
		})
	}
	if len(versions) > 1 {
		t.Run("upgrade keeps sessions", func(t *testing.T) {
			upgradeHome := t.TempDir()
			work := t.TempDir()
			if err := os.WriteFile(filepath.Join(upgradeHome, "config.toml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := run(ctx, versions[0], upgradeHome, work, "codex", "exec", "--skip-git-repo-check", "--sandbox", "read-only", "-C", "/workspaces/compat", "-o", "/codex-home/upgrade-first.txt", "Remember the codeword kiwi-42. Reply exactly ready."); err != nil {
				t.Fatal(err)
			}
			if err := run(ctx, versions[len(versions)-1], upgradeHome, work, "codex", "exec", "--skip-git-repo-check", "resume", "--last", "-o", "/codex-home/upgrade-last.txt", "What codeword did I ask you to remember? Reply with only the codeword."); err != nil {
				t.Fatal(err)
			}
			message, err := os.ReadFile(filepath.Join(upgradeHome, "upgrade-last.txt"))
			if err != nil || strings.TrimSpace(string(message)) != "kiwi-42" {
				t.Fatal("Codex lost the session across the CLI upgrade")
			}
			assertSecretsAbsent(t, append(before, codexAuthSecrets(t, authPath)...), upgradeHome, work)
		})
	}
	request, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com/backend-api/codex/models", nil)
	request.Header.Set("Authorization", "Bearer "+placeholder)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("provider answered copied placeholder with %d", response.StatusCode)
	}
	after := codexAuthSecrets(t, authPath)
	if bytes.Equal(before[0], after[0]) || bytes.Equal(before[1], after[1]) {
		t.Fatal("native host Codex refresh did not rotate both tokens")
	}
	assertSecretsAbsent(t, append(before, after...), home, dir)
	events, err := os.ReadFile(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range append(before, after...) {
		if bytes.Contains(events, secret) {
			t.Fatal("model proxy evidence contains a host credential")
		}
	}
	t.Run("broker down stops model access", func(t *testing.T) {
		_ = server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		work := t.TempDir()
		err := run(ctx, versions[len(versions)-1], home, work, "codex", "exec", "--skip-git-repo-check", "--sandbox", "read-only", "-C", "/workspaces/compat", "-o", "/codex-home/broker-down.txt", "Reply exactly AI_CREW_BROKER_DOWN_OK.")
		if err == nil || ctx.Err() != nil {
			t.Fatalf("Codex did not stop within 45 seconds after broker shutdown: %v", err)
		}
	})
}

func buildCodexImage(t *testing.T, version string) {
	t.Helper()
	cmd := exec.Command("podman", "build", "-q", "--build-arg", "CODEX_VERSION="+version, "-t", codexImage(version), filepath.Join("testdata", "codex"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Codex %s image: %v\n%s", version, err, out)
	}
}

func codexImage(version string) string { return "localhost/ai-crew-modelproxy-codex:" + version }

func codexAuthSecrets(t *testing.T, path string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cache struct {
		Tokens struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &cache); err != nil || cache.Tokens.Access == "" || cache.Tokens.Refresh == "" {
		t.Fatal("host Codex login cache is incomplete")
	}
	return [][]byte{[]byte(cache.Tokens.Access), []byte(cache.Tokens.Refresh)}
}

func assertSecretsAbsent(t *testing.T, secrets [][]byte, roots ...string) {
	t.Helper()
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, secret := range secrets {
				if bytes.Contains(data, secret) {
					t.Errorf("host credential found in %s", path)
				}
			}
			return nil
		}); err != nil {
			t.Error(err)
		}
	}
}
