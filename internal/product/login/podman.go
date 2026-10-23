package login

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type stdio struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

type container struct {
	name        string
	image       string
	staging     string
	interactive bool
	env         []string
	command     []string
}

func (s *Service) podman(ctx context.Context, budget time.Duration, streams stdio, env []string, args ...string) error {
	commandCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	command := exec.CommandContext(commandCtx, "podman", args...)
	command.Stdin = streams.in
	command.Stdout = streams.out
	command.Stderr = streams.err
	command.Env = env
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = terminationGrace
	err := command.Run()
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("deadline exceeded after %s", budget)
	}
	return err
}

func (c container) args() []string {
	args := []string{"run", "--rm"}
	if c.interactive {
		args = append(args, "--interactive", "--tty")
	}
	args = append(args,
		"--name", c.name,
		"--pull=never",
		"--read-only",
		"--security-opt", "no-new-privileges",
		"--cap-drop", "all",
		"--pids-limit", "256",
		"--memory", "1g",
		"--userns", "keep-id",
		"--user", strconv.Itoa(os.Geteuid())+":"+strconv.Itoa(os.Getegid()),
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=64m",
		"--volume", c.staging+":/login:rw,Z",
		"--env", "HOME=/login",
		"--env", "CODEX_HOME=/login",
	)
	for _, variable := range c.env {
		args = append(args, "--env", variable)
	}
	args = append(args, c.image)
	return append(args, c.command...)
}

func (s *Service) runContainer(ctx context.Context, budget time.Duration, spec container, streams stdio, env []string) error {
	if err := s.podman(ctx, budget, streams, env, spec.args()...); err != nil {
		return errors.Join(err, s.removeContainer(spec.name))
	}
	return nil
}

func (s *Service) removeContainer(name string) error {
	if err := s.podman(context.Background(), cleanupBudget, stdio{out: io.Discard, err: s.console.Err}, nil, "rm", "--force", "--ignore", name); err != nil {
		return fmt.Errorf("clean up login container: %w", err)
	}
	return nil
}

func containerName(purpose string) (string, error) {
	suffix, err := randomHex(8)
	if err != nil {
		return "", fmt.Errorf("create login container name: %w", err)
	}
	return "ai-login-" + purpose + "-" + suffix, nil
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
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
		_, _ = w.buffer.Write(data[:min(len(data), remaining)])
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
