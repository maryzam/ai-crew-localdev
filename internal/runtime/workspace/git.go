package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	gitCommandTimeout = 5 * time.Minute
	gitOutputLimit    = 64 << 10
)

type gitRunner struct {
	path string
}

func newGitRunner() gitRunner {
	return gitRunner{path: "git"}
}

func (runner gitRunner) run(ctx context.Context, directory string, args ...string) (string, error) {
	commandArgs := []string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "credential.helper=",
		"-c", "credential.interactive=never",
		"-c", "protocol.allow=never",
	}
	if directory != "" {
		commandArgs = append(commandArgs, "-C", directory)
	}
	commandArgs = append(commandArgs, args...)
	commandCtx, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, runner.path, commandArgs...)
	command.Env = gitEnvironment()
	var output boundedBuffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	text := strings.TrimSpace(output.String())
	if commandCtx.Err() != nil {
		return text, fmt.Errorf("git %s exceeded %s: %w", strings.Join(commandArgs, " "), gitCommandTimeout, commandCtx.Err())
	}
	if err != nil {
		if text == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(commandArgs, " "), err)
		}
		return text, fmt.Errorf("git %s: %w: %s", strings.Join(commandArgs, " "), err, text)
	}
	return text, nil
}

func (runner gitRunner) runLocal(ctx context.Context, directory string, args ...string) (string, error) {
	return runner.run(ctx, directory, append([]string{"-c", "protocol.file.allow=always"}, args...)...)
}

func (runner gitRunner) succeeds(ctx context.Context, directory string, args ...string) (bool, error) {
	_, err := runner.run(ctx, directory, args...)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func gitEnvironment() []string {
	values := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GCM_INTERACTIVE=never",
		"SSH_ASKPASS=/bin/false",
		"GIT_SSH_COMMAND=false",
		"LANG=C",
		"LC_ALL=C",
	}
	for _, name := range []string{"PATH", "TMPDIR", "TEMP", "TMP"} {
		if value := os.Getenv(name); value != "" {
			values = append(values, name+"="+value)
		}
	}
	return values
}

type boundedBuffer struct {
	buffer bytes.Buffer
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	room := gitOutputLimit - buffer.buffer.Len()
	if len(data) > room {
		data = data[:max(room, 0)]
	}
	if len(data) > 0 {
		_, _ = buffer.buffer.Write(data)
	}
	return original, nil
}

func (buffer *boundedBuffer) String() string {
	return buffer.buffer.String()
}
