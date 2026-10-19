package modelproxy

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
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
	Invalidate(string)
}

type codexAuthState struct {
	credential  CodexCredential
	refreshHash [sha256.Size]byte
	expires     time.Time
}

type refreshAttempt struct {
	done chan struct{}
	err  error
}

type CodexAuthSource struct {
	path           string
	binary         string
	binaryHash     [sha256.Size]byte
	recorder       Recorder
	refresh        func(context.Context) error
	refreshTimeout time.Duration
	now            func() time.Time
	mu             sync.Mutex
	inFlight       *refreshAttempt
	invalidated    map[string]struct{}
	attempts       []time.Time
	generation     uint64
	evidenceBroken bool
	unusable       bool
}

const (
	codexRefreshTimeout  = 20 * time.Second
	codexRefreshInterval = time.Minute
	codexRefreshWindow   = time.Hour
	codexRefreshLimit    = 3
)

func NewCodexAuthSource(authPath, codexBinary, binarySHA256 string, recorder Recorder) (*CodexAuthSource, error) {
	if !filepath.IsAbs(authPath) || !filepath.IsAbs(codexBinary) || recorder == nil || filepath.Base(authPath) != "auth.json" {
		return nil, errors.New("codex auth needs absolute host paths and an evidence recorder")
	}
	digest, err := hex.DecodeString(binarySHA256)
	if err != nil || len(digest) != sha256.Size {
		return nil, errors.New("codex refresh needs a pinned binary SHA-256")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(authPath))
	if err != nil {
		return nil, errors.New("cannot resolve Codex broker auth directory")
	}
	if err := privateBrokerDir(parent, "Codex broker auth"); err != nil {
		return nil, err
	}
	path := filepath.Join(parent, "auth.json")
	if _, err := readCodexAuth(path); err != nil {
		return nil, err
	}
	binary, err := filepath.EvalSymlinks(codexBinary)
	if err != nil {
		return nil, errors.New("cannot resolve pinned Codex binary")
	}
	if filepath.Dir(binary) == parent {
		return nil, errors.New("codex refresh binary must be separate from the broker login")
	}
	s := &CodexAuthSource{path: path, binary: binary, recorder: recorder, now: time.Now, refreshTimeout: codexRefreshTimeout, invalidated: make(map[string]struct{})}
	copy(s.binaryHash[:], digest)
	file, err := s.openPinnedBinary()
	if err != nil {
		return nil, err
	}
	_ = file.Close()
	s.refresh = s.refreshWithNativeCLI
	return s, nil
}

func privateBrokerDir(path, purpose string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return fmt.Errorf("%s directory must not change or follow a symlink", purpose)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s directory must be private", purpose)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%s directory must be owned by the broker user", purpose)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.New("cannot locate operator home")
	}
	defaultHome, err := filepath.EvalSymlinks(filepath.Join(home, ".codex"))
	if err == nil && (path == defaultHome || strings.HasPrefix(path, defaultHome+string(os.PathSeparator))) {
		return fmt.Errorf("%s must be separate from the operator Codex home", purpose)
	}
	if selected := os.Getenv("CODEX_HOME"); selected != "" {
		selectedHome, err := filepath.EvalSymlinks(selected)
		if err == nil && (path == selectedHome || strings.HasPrefix(path, selectedHome+string(os.PathSeparator))) {
			return fmt.Errorf("%s must be separate from the selected Codex home", purpose)
		}
	}
	return nil
}

func (s *CodexAuthSource) Resolve(ctx context.Context) (CodexCredential, error) {
	for {
		if err := ctx.Err(); err != nil {
			return CodexCredential{}, err
		}
		if err := privateBrokerDir(filepath.Dir(s.path), "Codex broker auth"); err != nil {
			return CodexCredential{}, err
		}
		s.mu.Lock()
		if s.evidenceBroken || s.unusable {
			s.mu.Unlock()
			return CodexCredential{}, errors.New("codex broker login is unavailable")
		}
		if attempt := s.inFlight; attempt != nil {
			s.mu.Unlock()
			if err := waitForRefresh(ctx, attempt); err != nil {
				return CodexCredential{}, err
			}
			continue
		}
		generation := s.generation
		s.mu.Unlock()
		state, err := readCodexAuth(s.path)
		s.mu.Lock()
		if generation != s.generation || s.inFlight != nil {
			attempt := s.inFlight
			s.mu.Unlock()
			if attempt != nil {
				if err := waitForRefresh(ctx, attempt); err != nil {
					return CodexCredential{}, err
				}
			}
			continue
		}
		if err != nil {
			s.mu.Unlock()
			return CodexCredential{}, err
		}
		if s.evidenceBroken || s.unusable {
			s.mu.Unlock()
			return CodexCredential{}, errors.New("codex broker login is unavailable")
		}
		for token := range s.invalidated {
			if token != state.credential.accessToken {
				delete(s.invalidated, token)
			}
		}
		_, rejected := s.invalidated[state.credential.accessToken]
		if !rejected && state.expires.After(s.now().Add(5*time.Minute)) {
			s.mu.Unlock()
			return state.credential, nil
		}
		now := s.now()
		recent := s.attempts[:0]
		for _, at := range s.attempts {
			if now.Sub(at) < codexRefreshWindow {
				recent = append(recent, at)
			}
		}
		s.attempts = recent
		if len(recent) >= codexRefreshLimit || len(recent) > 0 && now.Sub(recent[len(recent)-1]) < codexRefreshInterval {
			usable := !rejected && state.expires.After(now.Add(10*time.Second))
			decision := Refused
			if usable {
				decision = RefreshDeferred
			}
			err := s.record(Event{Decision: decision, Method: "AUTH", Path: "codex", Reason: "Codex refresh budget exceeded"})
			s.mu.Unlock()
			if err != nil {
				return CodexCredential{}, err
			}
			if usable {
				return state.credential, nil
			}
			return CodexCredential{}, errors.New("codex refresh budget exceeded")
		}
		if err := s.record(Event{Decision: RefreshRequested, Method: "AUTH", Path: "codex"}); err != nil {
			s.mu.Unlock()
			return CodexCredential{}, err
		}
		s.attempts = append(s.attempts, now)
		attempt := &refreshAttempt{done: make(chan struct{})}
		s.inFlight = attempt
		s.mu.Unlock()
		go s.runRefresh(context.WithoutCancel(ctx), state, attempt)
		if err := waitForRefresh(ctx, attempt); err != nil {
			return CodexCredential{}, err
		}
	}
}

func waitForRefresh(ctx context.Context, attempt *refreshAttempt) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-attempt.done:
		return attempt.err
	}
}

func (s *CodexAuthSource) Invalidate(token string) {
	s.mu.Lock()
	if token != "" {
		s.invalidated[token] = struct{}{}
	}
	s.mu.Unlock()
}

func (s *CodexAuthSource) runRefresh(parent context.Context, before codexAuthState, attempt *refreshAttempt) {
	ctx, cancel := context.WithTimeout(parent, s.refreshTimeout)
	defer cancel()
	err := s.refresh(ctx)
	after, readErr := readCodexAuth(s.path)
	accessChanged := readErr == nil && after.credential.accessToken != before.credential.accessToken
	refreshChanged := readErr == nil && after.refreshHash != before.refreshHash
	rotated := accessChanged && refreshChanged && after.credential.accountID == before.credential.accountID && after.expires.After(s.now().Add(time.Minute))
	if rotated {
		err = nil
	} else if readErr != nil {
		err = readErr
	} else if accessChanged || refreshChanged || err == nil {
		err = errors.New("native Codex refresh did not persist both tokens for the same account")
	}
	s.mu.Lock()
	if err != nil && (accessChanged || refreshChanged) {
		s.unusable = true
	}
	decision := RefreshRotated
	if err != nil {
		decision = RefreshFailed
	}
	if recordErr := s.record(Event{Decision: decision, Method: "AUTH", Path: "codex"}); recordErr != nil {
		err = recordErr
	}
	if err == nil {
		delete(s.invalidated, before.credential.accessToken)
	}
	_, rejected := s.invalidated[before.credential.accessToken]
	if err != nil && !s.unusable && !rejected && before.expires.After(s.now().Add(10*time.Second)) {
		attempt.err = nil
	} else {
		attempt.err = err
	}
	s.inFlight = nil
	s.generation++
	close(attempt.done)
	s.mu.Unlock()
}

func (s *CodexAuthSource) record(event Event) error {
	if err := s.recorder.Record(event); err != nil {
		s.evidenceBroken = true
		return errors.New("codex refresh evidence is unavailable")
	}
	return nil
}

func readCodexAuth(path string) (codexAuthState, error) {
	data, err := readOwnerOnlyFile(path, 256<<10)
	if err != nil {
		return codexAuthState{}, err
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
		return codexAuthState{}, errors.New("codex auth file lacks a complete ChatGPT subscription login")
	}
	expires, err := codexJWTExpiry(cache.Tokens.Access)
	if err != nil {
		return codexAuthState{}, err
	}
	return codexAuthState{credential: CodexCredential{accessToken: cache.Tokens.Access, accountID: cache.Tokens.Account}, refreshHash: sha256.Sum256([]byte(cache.Tokens.Refresh)), expires: expires}, nil
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
	if err := privateBrokerDir(filepath.Dir(s.path), "Codex broker auth"); err != nil {
		return err
	}
	binary, err := s.openPinnedBinary()
	if err != nil {
		return err
	}
	defer func() { _ = binary.Close() }()
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", "app-server")
	cmd.ExtraFiles = []*os.File{binary}
	cmd.WaitDelay = 2 * time.Second
	home := filepath.Dir(s.path)
	cmd.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + home, "PATH=/usr/bin:/bin", "CODEX_HOME=" + home}
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
	waited := false
	defer func() {
		if !waited {
			_ = stdin.Close()
			_ = cmd.Wait()
		}
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
			if err := stdin.Close(); err != nil {
				return errors.New("cannot close native Codex refresh input")
			}
			_, drainErr := io.Copy(io.Discard, stdout)
			err := cmd.Wait()
			waited = true
			if err != nil || drainErr != nil {
				return errors.New("native Codex refresh did not exit cleanly")
			}
			return nil
		}
	}
	return errors.New("native Codex refresh ended without a result")
}

func (s *CodexAuthSource) openPinnedBinary() (*os.File, error) {
	if err := privateBrokerDir(filepath.Dir(s.binary), "Codex refresh binary"); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(s.binary, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("cannot open pinned Codex binary")
	}
	file := os.NewFile(uintptr(fd), s.binary)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Mode().Perm()&0o111 == 0 || info.Size() > 200<<20 {
		_ = file.Close()
		return nil, errors.New("pinned Codex binary is unsafe")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) && owner.Uid != 0 {
		_ = file.Close()
		return nil, errors.New("pinned Codex binary has an untrusted owner")
	}
	hash := sha256.New()
	var magic [4]byte
	if _, err := io.ReadFull(file, magic[:]); err != nil || string(magic[:]) != "\x7fELF" {
		_ = file.Close()
		return nil, errors.New("pinned Codex refresh program must be an ELF executable")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, errors.New("cannot inspect pinned Codex binary")
	}
	if _, err := io.Copy(hash, file); err != nil || subtle.ConstantTimeCompare(hash.Sum(nil), s.binaryHash[:]) != 1 {
		_ = file.Close()
		return nil, errors.New("pinned Codex binary changed")
	}
	return file, nil
}
