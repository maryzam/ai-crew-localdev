package login

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
	"github.com/maryzam/ai-crew-localdev/internal/product/securefile"
)

const claudeTokenVariable = "CLAUDE_CODE_OAUTH_TOKEN"

var claudeProbeCommand = []string{"claude", "-p", "Reply with OK", "--output-format", "json", "--no-session-persistence", "--tools", ""}

type staged struct {
	credential []byte
	binaryHash string
	image      loginImage
}

type stageError struct {
	stage string
	err   error
}

func (e stageError) Error() string { return e.err.Error() }
func (e stageError) Unwrap() error { return e.err }

func failAt(stage string, err error) error {
	return stageError{stage: stage, err: err}
}

func (s *Service) stage(ctx context.Context, agent agents.Agent, paths Paths) (staged, error) {
	pin, err := s.refreshPin(agent)
	if err != nil {
		return staged{}, failAt("refresh_binary", err)
	}
	if _, err := exec.LookPath("podman"); err != nil {
		return staged{}, failAt("podman_unavailable", fmt.Errorf("podman is required: %w", err))
	}
	if err := s.podman(ctx, podmanHealthBudget, stdio{out: io.Discard, err: s.console.Err}, nil, "info", "--format", "json"); err != nil {
		return staged{}, failAt("podman_unhealthy", fmt.Errorf("podman is unavailable: %w", err))
	}
	image, err := s.prepareImage(ctx)
	if err != nil {
		return staged{}, failAt("image", err)
	}
	staging, err := os.MkdirTemp(paths.LoginRoot, "."+agent.Name+"-staging-")
	if err != nil {
		return staged{}, failAt("staging", fmt.Errorf("create login staging directory: %w", err))
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := os.Chmod(staging, 0o700); err != nil {
		return staged{}, failAt("staging", fmt.Errorf("secure login staging directory: %w", err))
	}
	result := staged{image: image}
	switch agent.Login {
	case agents.Claude:
		result.credential, err = s.claudeLogin(ctx, agent, image, staging)
	case agents.Codex:
		result.credential, err = s.codexLogin(ctx, agent, image, staging)
	default:
		err = failAt("registry", errors.New("agent registry has an unsupported login mechanism"))
	}
	if err != nil {
		return staged{}, err
	}
	if agent.Refresh != nil {
		if err := s.installRefreshBinary(ctx, agent, image, paths, pin); err != nil {
			return staged{}, failAt("refresh_binary", err)
		}
		result.binaryHash = pin
	}
	return result, nil
}

func (s *Service) nativeLogin(ctx context.Context, agent agents.Agent, image loginImage, staging string, streams stdio) error {
	name, err := containerName(agent.Name)
	if err != nil {
		return failAt("container", err)
	}
	spec := container{name: name, image: image.id, staging: staging, interactive: true, command: agent.LoginCommand}
	if err := s.runContainer(ctx, loginBudget, spec, streams, nil); err != nil {
		return failAt("native_login", fmt.Errorf("%s login failed: %w", agent.Name, err))
	}
	return nil
}

func (s *Service) claudeLogin(ctx context.Context, agent agents.Agent, image loginImage, staging string) ([]byte, error) {
	output := newBoundedOutput(claudeOutputLimit)
	streams := stdio{in: s.console.In, out: io.MultiWriter(s.console.Out, output), err: io.MultiWriter(s.console.Err, output)}
	if err := s.nativeLogin(ctx, agent, image, staging, streams); err != nil {
		return nil, err
	}
	if output.overflowed() {
		return nil, failAt("validation", fmt.Errorf("claude login output exceeded %d KiB", claudeOutputLimit>>10))
	}
	token, err := claudeCredential(output.bytes())
	if err != nil {
		return nil, failAt("validation", err)
	}
	if err := s.probeClaude(ctx, image, staging, token); err != nil {
		return nil, failAt("probe", err)
	}
	return append(token, '\n'), nil
}

func (s *Service) probeClaude(ctx context.Context, image loginImage, staging string, token []byte) error {
	_, _ = fmt.Fprintln(s.console.Err, "Verifying the Claude setup-token with one native request")
	name, err := containerName("claude-probe")
	if err != nil {
		return err
	}
	spec := container{name: name, image: image.id, staging: staging, env: []string{claudeTokenVariable, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1"}, command: claudeProbeCommand}
	output := newBoundedOutput(claudeOutputLimit)
	env := append(os.Environ(), claudeTokenVariable+"="+string(token))
	if err := s.runContainer(ctx, probeBudget, spec, stdio{out: output, err: s.console.Err}, env); err != nil {
		return fmt.Errorf("claude could not complete a request with the captured setup-token: %w", err)
	}
	var result struct {
		IsError *bool `json:"is_error"`
	}
	if output.overflowed() || json.Unmarshal(bytes.TrimSpace(output.bytes()), &result) != nil || result.IsError == nil || *result.IsError {
		return errors.New("claude did not complete a request with the captured setup-token")
	}
	return nil
}

func (s *Service) codexLogin(ctx context.Context, agent agents.Agent, image loginImage, staging string) ([]byte, error) {
	streams := stdio{in: s.console.In, out: s.console.Out, err: s.console.Err}
	if err := s.nativeLogin(ctx, agent, image, staging, streams); err != nil {
		return nil, err
	}
	credential, err := codexCredential(filepath.Join(staging, agent.CredentialFile), agent.CredentialLimit, s.now())
	if err != nil {
		return nil, failAt("validation", err)
	}
	return credential, nil
}

func (s *Service) refreshPin(agent agents.Agent) (string, error) {
	if agent.Refresh == nil {
		return "", nil
	}
	pin := agent.Refresh.SHA256[s.arch]
	if pin == "" {
		return "", fmt.Errorf("no pinned %s refresh binary for %s", agent.Name, s.arch)
	}
	return pin, nil
}

func (s *Service) installRefreshBinary(ctx context.Context, agent agents.Agent, image loginImage, paths Paths, pin string) error {
	if hash, err := securefile.HashOwnerOnly(paths.Binary, agent.Refresh.SizeLimit); err == nil && hash == pin {
		return nil
	}
	name, err := containerName(agent.Name + "-refresh")
	if err != nil {
		return err
	}
	if err := s.podman(ctx, podmanHealthBudget, stdio{out: io.Discard, err: s.console.Err}, nil, "create", "--name", name, "--pull=never", image.id); err != nil {
		return fmt.Errorf("create %s refresh binary extraction container: %w", agent.Name, err)
	}
	defer func() { _ = s.removeContainer(name) }()
	suffix, err := randomHex(8)
	if err != nil {
		return err
	}
	temporary := filepath.Join(paths.BinaryDir, "."+agent.Refresh.FileName+".tmp-"+suffix)
	defer func() { _ = os.Remove(temporary) }()
	if err := s.podman(ctx, refreshCopyBudget, stdio{out: io.Discard, err: s.console.Err}, nil, "cp", name+":"+agent.Refresh.ImagePath, temporary); err != nil {
		return fmt.Errorf("extract %s refresh binary: %w", agent.Name, err)
	}
	if err := os.Chmod(temporary, 0o700); err != nil {
		return fmt.Errorf("secure %s refresh binary: %w", agent.Name, err)
	}
	hash, err := securefile.HashOwnerOnly(temporary, agent.Refresh.SizeLimit)
	if err != nil {
		return fmt.Errorf("read %s refresh binary: %w", agent.Name, err)
	}
	if hash != pin {
		return fmt.Errorf("%s refresh binary SHA-256 %s does not match the pinned %s", agent.Name, hash, pin)
	}
	if err := os.Rename(temporary, paths.Binary); err != nil {
		return fmt.Errorf("install %s refresh binary: %w", agent.Name, err)
	}
	return securefile.SyncDirectory(paths.BinaryDir)
}
