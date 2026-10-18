package modelproxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type CodexCredential struct {
	accessToken string
	accountID   string
}

func (CodexCredential) String() string   { return "[redacted]" }
func (CodexCredential) GoString() string { return "modelproxy.CodexCredential{[redacted]}" }

type CodexCredentialSource interface {
	Resolve(context.Context) (CodexCredential, error)
	Invalidate()
}

type CodexAuthSource struct {
	path        string
	binary      string
	recorder    Recorder
	refresh     func(context.Context) error
	mu          sync.Mutex
	invalidated bool
}

func NewCodexAuthSource(authPath, codexBinary string, recorder Recorder) (*CodexAuthSource, error) {
	if !filepath.IsAbs(authPath) || !filepath.IsAbs(codexBinary) || recorder == nil {
		return nil, errors.New("codex auth needs absolute host paths and an evidence recorder")
	}
	s := &CodexAuthSource{path: authPath, binary: codexBinary, recorder: recorder}
	s.refresh = s.refreshWithNativeCLI
	return s, nil
}

func (s *CodexAuthSource) Resolve(ctx context.Context) (CodexCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, expires, err := readCodexAuth(s.path)
	if err != nil {
		return CodexCredential{}, err
	}
	if s.invalidated || !expires.After(time.Now().Add(5*time.Minute)) {
		if err := s.recorder.Record(Event{Decision: RefreshRequested, Method: "AUTH", Path: "codex"}); err != nil {
			return CodexCredential{}, errors.New("codex refresh evidence is unavailable")
		}
		if err := s.refresh(ctx); err != nil {
			return CodexCredential{}, fmt.Errorf("native Codex refresh failed: %w", err)
		}
		credential, expires, err = readCodexAuth(s.path)
		if err != nil {
			return CodexCredential{}, err
		}
		if !expires.After(time.Now().Add(time.Minute)) {
			return CodexCredential{}, errors.New("refreshed Codex access token expires too soon")
		}
		s.invalidated = false
	}
	return credential, nil
}

func (s *CodexAuthSource) Invalidate() {
	s.mu.Lock()
	s.invalidated = true
	s.mu.Unlock()
}

func readCodexAuth(path string) (CodexCredential, time.Time, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return CodexCredential{}, time.Time{}, fmt.Errorf("open Codex auth file: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 256<<10 {
		return CodexCredential{}, time.Time{}, errors.New("codex auth file must be a private regular file under 256 KiB")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return CodexCredential{}, time.Time{}, errors.New("codex auth file must be owned by the broker user")
	}
	data, err := io.ReadAll(io.LimitReader(f, 256<<10+1))
	if err != nil || len(data) > 256<<10 {
		return CodexCredential{}, time.Time{}, errors.New("cannot read private Codex auth file")
	}
	var cache struct {
		AuthMode string `json:"auth_mode"`
		Tokens   struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &cache); err != nil || cache.AuthMode != "chatgpt" || cache.Tokens.Access == "" || cache.Tokens.Refresh == "" || cache.Tokens.Account == "" {
		return CodexCredential{}, time.Time{}, errors.New("codex auth file lacks a complete ChatGPT subscription login")
	}
	expires, err := codexJWTExpiry(cache.Tokens.Access)
	if err != nil {
		return CodexCredential{}, time.Time{}, err
	}
	return CodexCredential{accessToken: cache.Tokens.Access, accountID: cache.Tokens.Account}, expires, nil
}

func codexJWTExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, errors.New("codex access token has no JWT expiry")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, errors.New("codex access token has invalid JWT expiry")
	}
	var claims struct {
		Expires int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Expires <= 0 {
		return time.Time{}, errors.New("codex access token has invalid JWT expiry")
	}
	return time.Unix(claims.Expires, 0), nil
}

func (s *CodexAuthSource) refreshWithNativeCLI(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.binary, "app-server")
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.New("cannot locate host home for Codex refresh")
	}
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "CODEX_HOME=" + filepath.Dir(s.path)}
	for _, name := range []string{"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "SSL_CERT_FILE", "CODEX_CA_CERTIFICATE"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.New("cannot start Codex refresh input")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.New("cannot start Codex refresh output")
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return errors.New("cannot start native Codex refresh")
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	send := func(message any) error {
		return json.NewEncoder(stdin).Encode(message)
	}
	if err := send(map[string]any{"method": "initialize", "id": 1, "params": map[string]any{"clientInfo": map[string]string{"name": "ai_crew", "title": "AI Crew", "version": "0.0.0"}}}); err != nil {
		return errors.New("cannot initialize native Codex refresh")
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var response struct {
			ID     int             `json:"id"`
			Error  json.RawMessage `json:"error"`
			Result struct {
				Account struct {
					Type string `json:"type"`
				} `json:"account"`
			} `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &response) != nil {
			continue
		}
		if response.ID == 1 {
			if len(response.Error) != 0 {
				return errors.New("native Codex initialization was rejected")
			}
			if err := send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
				return errors.New("cannot complete native Codex initialization")
			}
			if err := send(map[string]any{"method": "account/read", "id": 2, "params": map[string]bool{"refreshToken": true}}); err != nil {
				return errors.New("cannot request native Codex refresh")
			}
		}
		if response.ID == 2 {
			if len(response.Error) != 0 || response.Result.Account.Type != "chatgpt" {
				return errors.New("native Codex did not refresh the ChatGPT login")
			}
			return nil
		}
	}
	return errors.New("native Codex refresh ended without a result")
}
