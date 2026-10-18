//go:build integration

package modelproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	source, err := NewCodexAuthSource(authPath, codexBinary, binaryHash, recorder)
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
	socket := filepath.Join(dir, "model.sock")
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
	source.Invalidate()
	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			result := filepath.Join(home, "last-message.txt")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			name := "ai-crew-codex-compat-" + hex.EncodeToString(nonce[:4])
			args := []string{"run", "--rm", "--name", name, "--network", "none", "--userns", "keep-id",
				"-v", dir + ":/run/model", "-v", home + ":/codex-home",
				"-e", "CODEX_HOME=/codex-home", "-e", "AI_CREW_PLACEHOLDER=" + placeholder,
				codexImage(version), "codex", "exec", "--skip-git-repo-check", "--sandbox", "read-only", "--color", "never", "-C", "/workspaces/compat", "-o", "/codex-home/last-message.txt", "Reply exactly AI_CREW_CODEX_OK and do not use tools."}
			cmd := exec.CommandContext(ctx, "podman", args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("Codex %s did not complete through the run socket: %v, stderr bytes %d", version, err, stderr.Len())
			}
			message, err := os.ReadFile(result)
			if err != nil || strings.TrimSpace(string(message)) != "AI_CREW_CODEX_OK" {
				t.Fatalf("Codex %s did not return the expected answer", version)
			}
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
