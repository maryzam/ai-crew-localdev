package uphost

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"time"
)

type brokerSupervisor struct {
	SocketPath string
	Stderr     io.Writer
	Resolve    func(string) (string, error)
	LookPath   func(string) (string, error)
	Timeout    time.Duration
	Dial       func(context.Context, string, string) (net.Conn, error)
}

func newBrokerSupervisor(socketPath string, stderr io.Writer, resolve func(string) (string, error)) brokerSupervisor {
	dialer := &net.Dialer{Timeout: time.Second}
	return brokerSupervisor{SocketPath: socketPath, Stderr: stderr, Resolve: resolve, LookPath: exec.LookPath, Timeout: 5 * time.Second, Dial: dialer.DialContext}
}

func EnsureBroker(ctx context.Context, socketPath string, stderr io.Writer, resolve func(string) (string, error)) error {
	return newBrokerSupervisor(socketPath, stderr, resolve).ensureRunning(ctx)
}

func (s brokerSupervisor) ensureRunning(ctx context.Context) error {
	if s.responds(ctx) {
		return nil
	}
	if systemctl, err := s.LookPath("systemctl"); err == nil {
		_ = exec.CommandContext(ctx, systemctl, "--user", "start", "ai-agent-broker.socket").Run()
		if s.wait(ctx) {
			return nil
		}
	}
	broker, err := s.Resolve("ai-agent-broker")
	if err != nil {
		return fmt.Errorf("broker not running and ai-agent-broker not found: %w", err)
	}
	command := exec.CommandContext(ctx, broker)
	command.Stdout = s.Stderr
	command.Stderr = s.Stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start broker: %w", err)
	}
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	if s.wait(ctx) {
		return nil
	}
	if command.Process != nil {
		_ = command.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("broker did not become ready within %s at %s", s.Timeout, s.SocketPath)
}

func (s brokerSupervisor) responds(ctx context.Context) bool {
	connection, err := s.Dial(ctx, "unix", s.SocketPath)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func (s brokerSupervisor) wait(ctx context.Context) bool {
	timer := time.NewTimer(s.Timeout)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.responds(ctx) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-ticker.C:
		}
	}
}
