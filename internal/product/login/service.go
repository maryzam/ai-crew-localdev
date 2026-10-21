package login

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
)

const (
	podmanHealthBudget = 10 * time.Second
	imageBuildBudget   = 10 * time.Minute
	loginBudget        = 15 * time.Minute
	cleanupBudget      = 10 * time.Second
	claudeOutputLimit  = 64 << 10
)

type Service struct {
	in     io.Reader
	out    io.Writer
	errOut io.Writer
	now    func() time.Time
}

type Result struct {
	Agent         string
	Credential    string
	Image         string
	RefreshBinary string
	BinarySHA256  string
}

func New(in io.Reader, out, errOut io.Writer) *Service {
	return &Service{in: in, out: out, errOut: errOut, now: time.Now}
}

func (s *Service) Login(ctx context.Context, name string) (Result, error) {
	agent, ok := agents.Find(name)
	if !ok {
		return Result{}, fmt.Errorf("unsupported agent %q; choose claude or codex", name)
	}
	paths, err := ResolvePaths(agent)
	if err != nil {
		return Result{}, err
	}
	if err := preparePaths(paths); err != nil {
		return Result{}, err
	}
	audit, err := openAudit(paths.Audit)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = audit.close() }()
	started := s.now()
	if err := audit.record(auditEvent{Time: started.UTC(), Agent: agent.Name, Decision: "login_requested"}); err != nil {
		return Result{}, err
	}
	result, stage, err := s.login(ctx, agent, paths)
	duration := s.now().Sub(started).Milliseconds()
	if err != nil {
		auditErr := audit.record(auditEvent{Time: s.now().UTC(), Agent: agent.Name, Decision: "login_failed", Reason: stage, DurationMS: duration})
		if auditErr != nil {
			return Result{}, errors.Join(err, auditErr)
		}
		return Result{}, err
	}
	if err := audit.record(auditEvent{Time: s.now().UTC(), Agent: agent.Name, Decision: "credential_validated", Image: result.Image, BinaryHash: result.binaryHash, DurationMS: duration}); err != nil {
		return Result{}, err
	}
	if len(result.binary) != 0 {
		if err := writeExecutable(paths.Binary, result.binary); err != nil {
			publishErr := fmt.Errorf("publish Codex refresh binary: %w", err)
			auditErr := audit.record(auditEvent{Time: s.now().UTC(), Agent: agent.Name, Decision: "login_failed", Reason: "publish", Image: result.Image, BinaryHash: result.binaryHash, DurationMS: duration})
			return Result{}, errors.Join(publishErr, auditErr)
		}
	}
	if err := securefile.WriteOwnerOnly(paths.Credential, result.credential); err != nil {
		publishErr := fmt.Errorf("publish broker credential: %w", err)
		auditErr := audit.record(auditEvent{Time: s.now().UTC(), Agent: agent.Name, Decision: "login_failed", Reason: "publish", Image: result.Image, BinaryHash: result.binaryHash, DurationMS: duration})
		return Result{}, errors.Join(publishErr, auditErr)
	}
	if err := audit.record(auditEvent{Time: s.now().UTC(), Agent: agent.Name, Decision: "credential_published", Image: result.Image, BinaryHash: result.binaryHash, DurationMS: duration}); err != nil {
		return Result{}, err
	}
	resultValue := Result{Agent: agent.Name, Credential: paths.Credential, Image: result.Image, BinarySHA256: result.binaryHash}
	if len(result.binary) != 0 {
		resultValue.RefreshBinary = paths.Binary
	}
	return resultValue, nil
}

type stagedResult struct {
	credential []byte
	binary     []byte
	binaryHash string
	Image      string
}

func (s *Service) login(ctx context.Context, agent agents.Agent, paths Paths) (stagedResult, string, error) {
	if _, err := exec.LookPath("podman"); err != nil {
		return stagedResult{}, "podman_unavailable", fmt.Errorf("podman is required: %w", err)
	}
	if err := s.run(ctx, podmanHealthBudget, io.Discard, s.errOut, "info", "--format", "json"); err != nil {
		return stagedResult{}, "podman_unhealthy", fmt.Errorf("podman is unavailable: %w", err)
	}
	if err := s.ensureImage(ctx); err != nil {
		return stagedResult{}, "image", err
	}
	image, err := s.imageID(ctx)
	if err != nil {
		return stagedResult{}, "image", err
	}
	staging, err := os.MkdirTemp(paths.LoginRoot, "."+agent.Name+"-staging-")
	if err != nil {
		return stagedResult{}, "staging", fmt.Errorf("create login staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := os.Chmod(staging, 0o700); err != nil {
		return stagedResult{}, "staging", fmt.Errorf("secure login staging directory: %w", err)
	}
	output := newBoundedOutput(claudeOutputLimit)
	stdout := s.out
	stderr := s.errOut
	if agent.Login == agents.Claude {
		stdout = io.MultiWriter(s.out, output)
		stderr = io.MultiWriter(s.errOut, output)
	}
	name, err := containerName(agent.Name)
	if err != nil {
		return stagedResult{}, "container", err
	}
	args := s.containerArgs(agent, staging, name)
	if err := s.runInteractive(ctx, loginBudget, stdout, stderr, args...); err != nil {
		cleanupErr := s.cleanupContainer(name)
		return stagedResult{}, "native_login", errors.Join(fmt.Errorf("%s login failed: %w", agent.Name, err), cleanupErr)
	}
	var credential []byte
	switch agent.Login {
	case agents.Claude:
		if output.overflowed() {
			return stagedResult{}, "validation", errors.New("claude login output exceeded 64 KiB")
		}
		credential, err = claudeCredential(output.bytes())
	case agents.Codex:
		credential, err = codexCredential(filepath.Join(staging, agent.CredentialFile), s.now())
	default:
		err = errors.New("agent registry has an unsupported login mechanism")
	}
	if err != nil {
		return stagedResult{}, "validation", err
	}
	result := stagedResult{credential: credential, Image: image}
	if agent.Login == agents.Codex {
		result.binary, result.binaryHash, err = s.extractCodexBinary(ctx, staging)
		if err != nil {
			return stagedResult{}, "refresh_binary", err
		}
	}
	return result, "", nil
}

func (s *Service) extractCodexBinary(ctx context.Context, staging string) ([]byte, string, error) {
	name, err := containerName("codex-binary")
	if err != nil {
		return nil, "", err
	}
	var created bytes.Buffer
	if err := s.run(ctx, podmanHealthBudget, &created, s.errOut, "create", "--name", name, "--pull=never", agents.LoginImage); err != nil {
		return nil, "", fmt.Errorf("create Codex binary extraction container: %w", err)
	}
	defer func() { _ = s.cleanupContainer(name) }()
	path := filepath.Join(staging, "codex")
	if err := s.run(ctx, podmanHealthBudget, io.Discard, s.errOut, "cp", name+":/opt/ai-crew/codex", path); err != nil {
		return nil, "", fmt.Errorf("extract Codex refresh binary: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return nil, "", fmt.Errorf("secure Codex refresh binary: %w", err)
	}
	data, err := securefile.ReadOwnerOnly(path, 200<<20)
	if err != nil || len(data) < 4 || string(data[:4]) != "\x7fELF" {
		return nil, "", errors.New("pinned Codex refresh program must be an owner-only ELF executable")
	}
	digest := sha256.Sum256(data)
	return data, hex.EncodeToString(digest[:]), nil
}

func writeExecutable(path string, data []byte) error {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".codex.tmp-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = file.Close() }()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0o700); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return securefile.SyncDirectory(directory)
}

func (s *Service) ensureImage(ctx context.Context) error {
	if s.run(ctx, podmanHealthBudget, io.Discard, io.Discard, "image", "exists", agents.LoginImage) == nil {
		return nil
	}
	contextDir, err := os.MkdirTemp("", "ai-login-image-")
	if err != nil {
		return fmt.Errorf("create login image context: %w", err)
	}
	defer func() { _ = os.RemoveAll(contextDir) }()
	if err := os.WriteFile(filepath.Join(contextDir, "Containerfile"), loginContainerfile, 0o600); err != nil {
		return fmt.Errorf("prepare login image: %w", err)
	}
	if err := s.run(ctx, imageBuildBudget, s.out, s.errOut, "build", "--pull=missing", "--tag", agents.LoginImage, contextDir); err != nil {
		return fmt.Errorf("build pinned login image within %s: %w", imageBuildBudget, err)
	}
	return nil
}

func (s *Service) imageID(ctx context.Context) (string, error) {
	var output bytes.Buffer
	if err := s.run(ctx, podmanHealthBudget, &output, s.errOut, "image", "inspect", "--format", "{{.Id}}", agents.LoginImage); err != nil {
		return "", fmt.Errorf("inspect login image: %w", err)
	}
	id := string(bytes.TrimSpace(output.Bytes()))
	if id == "" {
		return "", errors.New("login image has no immutable ID")
	}
	return id, nil
}

func (s *Service) containerArgs(agent agents.Agent, staging, name string) []string {
	uid := strconv.Itoa(os.Geteuid())
	gid := strconv.Itoa(os.Getegid())
	args := []string{"run", "--rm", "--interactive", "--tty", "--name", name, "--pull=never", "--read-only", "--security-opt", "no-new-privileges", "--cap-drop", "all", "--pids-limit", "256", "--memory", "1g", "--userns", "keep-id", "--user", uid + ":" + gid, "--tmpfs", "/tmp:rw,nosuid,nodev,size=64m", "--volume", staging + ":/login:rw,Z", "--env", "HOME=/login", "--env", "CODEX_HOME=/login", agents.LoginImage}
	return append(args, agent.LoginCommand...)
}

func (s *Service) run(ctx context.Context, budget time.Duration, stdout, stderr io.Writer, args ...string) error {
	commandCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	command := exec.CommandContext(commandCtx, "podman", args...)
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if commandCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("deadline exceeded after %s", budget)
	}
	return err
}

func (s *Service) runInteractive(ctx context.Context, budget time.Duration, stdout, stderr io.Writer, args ...string) error {
	commandCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	command := exec.CommandContext(commandCtx, "podman", args...)
	command.Stdin = s.in
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if commandCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("deadline exceeded after %s", budget)
	}
	return err
}

func (s *Service) cleanupContainer(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	command := exec.CommandContext(ctx, "podman", "rm", "--force", "--ignore", name)
	command.Stdout = io.Discard
	command.Stderr = s.errOut
	if err := command.Run(); err != nil {
		return fmt.Errorf("clean up login container: %w", err)
	}
	return nil
}

func containerName(agent string) (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create login container name: %w", err)
	}
	return "ai-login-" + agent + "-" + hex.EncodeToString(value), nil
}

type boundedOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func newBoundedOutput(limit int) *boundedOutput {
	return &boundedOutput{limit: limit}
}

func (w *boundedOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		keep := len(data)
		if keep > remaining {
			keep = remaining
		}
		_, _ = w.buffer.Write(data[:keep])
	}
	if len(data) > remaining {
		w.overflow = true
	}
	return len(data), nil
}

func (w *boundedOutput) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buffer.Bytes()...)
}

func (w *boundedOutput) overflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflow
}
