package uphost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/devcontainer"
)

type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type CommandRunner func(context.Context, string, []string, Streams) error
type OutputRunner func(context.Context, string, []string) (string, error)

const containerCleanupTimeout = 30 * time.Second
const containerLookupOutputLimit = 16 << 10

var containerIDPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

func runCommand(ctx context.Context, name string, args []string, streams Streams) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = streams.In
	command.Stdout = streams.Out
	command.Stderr = streams.Err
	return command.Run()
}

type ContainerLauncher struct {
	Streams     Streams
	CommandOut  io.Writer
	CommandErr  io.Writer
	Progress    ProgressFunc
	Runner      CommandRunner
	Output      OutputRunner
	LookPath    func(string) (string, error)
	PrepareRoot func(workspace string) (string, error)
	Overlay     devcontainer.OverlayBuilder
}

func NewContainerLauncher(streams Streams, progress ProgressFunc) ContainerLauncher {
	return ContainerLauncher{
		Streams:    streams,
		CommandOut: streams.Out,
		CommandErr: streams.Err,
		Progress:   progress,
		Runner:     runCommand,
		Output: func(ctx context.Context, name string, args []string) (string, error) {
			command := exec.CommandContext(ctx, name, args...)
			var output limitedOutput
			command.Stdout = &output
			command.Stderr = &output
			err := command.Run()
			return strings.TrimSpace(output.String()), err
		},
		LookPath: exec.LookPath,
		PrepareRoot: func(workspace string) (string, error) {
			return devcontainer.PrepareGenericRoot(paths.DataDir(), workspace, os.Executable)
		},
		Overlay: devcontainer.NewOverlayBuilder(os.Executable),
	}
}

type limitedOutput struct {
	buffer bytes.Buffer
}

type capturedOutput struct {
	destination io.Writer
	output      tailOutput
}

func (capture *capturedOutput) Write(data []byte) (int, error) {
	_, _ = capture.output.Write(data)
	if capture.destination == nil {
		return len(data), nil
	}
	return capture.destination.Write(data)
}

func (capture *capturedOutput) containerID() string {
	var result struct {
		ContainerID string `json:"containerId"`
	}
	lines := strings.Split(capture.output.String(), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if json.Unmarshal([]byte(strings.TrimSpace(lines[index])), &result) == nil && containerIDPattern.MatchString(result.ContainerID) {
			return result.ContainerID
		}
	}
	return ""
}

type tailOutput struct {
	buffer bytes.Buffer
}

func (output *tailOutput) Write(data []byte) (int, error) {
	original := len(data)
	if len(data) >= containerLookupOutputLimit {
		output.buffer.Reset()
		_, _ = output.buffer.Write(data[len(data)-containerLookupOutputLimit:])
		return original, nil
	}
	overflow := output.buffer.Len() + len(data) - containerLookupOutputLimit
	if overflow > 0 {
		_ = output.buffer.Next(overflow)
	}
	_, _ = output.buffer.Write(data)
	return original, nil
}

func (output *tailOutput) String() string {
	return output.buffer.String()
}

func (output *limitedOutput) Write(data []byte) (int, error) {
	original := len(data)
	room := containerLookupOutputLimit - output.buffer.Len()
	if len(data) > room {
		data = data[:max(room, 0)]
	}
	if len(data) > 0 {
		_, _ = output.buffer.Write(data)
	}
	return original, nil
}

func (output *limitedOutput) String() string {
	return output.buffer.String()
}

func (l ContainerLauncher) FindCLI() (string, error) {
	return l.LookPath("devcontainer")
}

func (l ContainerLauncher) PrepareGenericRoot(workspace string) (string, error) {
	return l.PrepareRoot(workspace)
}

func (l ContainerLauncher) LaunchGeneric(ctx context.Context, devcontainerBin, workspace, target, runtimeName string, build bool) error {
	_, err := l.launchGeneric(ctx, devcontainerBin, workspace, target, runtimeName, build, []string{"bash"}, true, nil)
	return err
}

func (l ContainerLauncher) LaunchGenericCommand(ctx context.Context, devcontainerBin, workspace, target, runtimeName string, build bool, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("open managed session: command must not be empty")
	}
	_, err := l.launchGeneric(ctx, devcontainerBin, workspace, target, runtimeName, build, command, false, nil)
	return err
}

func (l ContainerLauncher) LaunchEphemeralGenericCommand(ctx context.Context, devcontainerBin, workspace, target, runtimeName string, build bool, command []string, started func(context.Context, string, string) error) (bool, error) {
	if len(command) == 0 {
		return true, fmt.Errorf("open managed session: command must not be empty")
	}
	containerID, launchErr := l.launchGeneric(ctx, devcontainerBin, workspace, target, runtimeName, build, command, false, started)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), containerCleanupTimeout)
	defer cancel()
	cleanupErr := l.removeGenericContainer(cleanupCtx, target, runtimeName, containerID)
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("%w; workspace changes were retained, rerun ai-agent start to recover them", cleanupErr)
	}
	return cleanupErr == nil, errors.Join(launchErr, cleanupErr)
}

func (l ContainerLauncher) ReconcileGenericContainer(ctx context.Context, target, runtimeName, containerID string) error {
	runtime, err := devcontainer.ParseRuntime(runtimeName)
	if err != nil {
		return err
	}
	if l.Output == nil {
		return fmt.Errorf("reconcile governed container: container resolver is not configured")
	}
	if containerID == "" {
		ids, resolveErr := l.Output(ctx, string(runtime), []string{"ps", "--all", "--quiet", "--filter", "label=devcontainer.local_folder=" + target})
		if resolveErr != nil {
			return fmt.Errorf("resolve governed container: %w", resolveErr)
		}
		containers := strings.Fields(ids)
		if len(containers) == 0 {
			return nil
		}
		if len(containers) != 1 || !containerIDPattern.MatchString(containers[0]) {
			return fmt.Errorf("resolve governed container: expected at most one valid container ID, found %d", len(containers))
		}
		containerID = containers[0]
	}
	removeErr := l.Runner(ctx, string(runtime), []string{"rm", "--force", containerID}, l.commandStreams())
	if removeErr == nil {
		return nil
	}
	ids, resolveErr := l.Output(ctx, string(runtime), []string{"ps", "--all", "--quiet", "--filter", "id=" + containerID})
	if resolveErr == nil && len(strings.Fields(ids)) == 0 {
		return nil
	}
	return fmt.Errorf("remove governed container: %w", removeErr)
}

func (l ContainerLauncher) removeGenericContainer(ctx context.Context, target, runtimeName, containerID string) error {
	runtime, err := devcontainer.ParseRuntime(runtimeName)
	if err != nil {
		return err
	}
	if containerID == "" {
		if l.Output == nil {
			return fmt.Errorf("remove governed container: container resolver is not configured")
		}
		ids, resolveErr := l.Output(ctx, string(runtime), []string{"ps", "--all", "--quiet", "--filter", "label=devcontainer.local_folder=" + target})
		if resolveErr != nil {
			return fmt.Errorf("resolve governed container: %w", resolveErr)
		}
		containers := strings.Fields(ids)
		if len(containers) != 1 || !containerIDPattern.MatchString(containers[0]) {
			return fmt.Errorf("resolve governed container: expected one valid container ID, found %d", len(containers))
		}
		containerID = containers[0]
	}
	if err := l.Runner(ctx, string(runtime), []string{"rm", "--force", containerID}, l.commandStreams()); err != nil {
		return fmt.Errorf("remove governed container: %w", err)
	}
	return nil
}

func (l ContainerLauncher) launchGeneric(ctx context.Context, devcontainerBin, workspace, target, runtimeName string, build bool, command []string, shell bool, started func(context.Context, string, string) error) (string, error) {
	runtime, err := devcontainer.ParseRuntime(runtimeName)
	if err != nil {
		return "", err
	}
	if started != nil {
		if err := started(ctx, string(runtime), ""); err != nil {
			return "", fmt.Errorf("record governed container intent: %w", err)
		}
	}
	l.report(Progress{Kind: GenericLaunching, Target: target, Runtime: runtimeName})
	capture := &capturedOutput{destination: l.CommandOut}
	upStreams := l.commandStreams()
	upStreams.Out = capture
	upErr := l.Runner(ctx, devcontainerBin, devcontainer.UpArgs(runtime, target, nil, build), upStreams)
	containerID := capture.containerID()
	if started != nil && containerID != "" {
		if err := started(ctx, string(runtime), containerID); err != nil {
			return containerID, fmt.Errorf("record governed container: %w", err)
		}
	}
	if upErr != nil {
		return containerID, fmt.Errorf("devcontainer up: %w", upErr)
	}
	reentry := devcontainer.ExecCommand(target, runtime)
	if shell {
		l.report(Progress{Kind: GenericReady, Target: target, Workspace: workspace, Runtime: runtimeName, Command: reentry})
	} else {
		l.report(Progress{Kind: ManagedWorkspaceReady})
	}
	l.runAuthStatus(ctx, devcontainerBin, devcontainer.ProjectExecArgs(runtime, target, nil, devcontainer.GenericAIAgentPath, "auth", "status"))
	if shell {
		l.report(Progress{Kind: ShellOpening})
	} else {
		l.report(Progress{Kind: AgentOpening})
	}
	args := devcontainer.ProjectExecArgs(runtime, target, nil, command...)
	if err := l.Runner(ctx, devcontainerBin, args, l.Streams); err != nil {
		if shell {
			return containerID, fmt.Errorf("open shell in devcontainer: %w (re-enter with: %s)", err, reentry)
		}
		return containerID, fmt.Errorf("run governed session in devcontainer: %w", err)
	}
	return containerID, nil
}

func (l ContainerLauncher) LaunchProject(ctx context.Context, devcontainerBin, project, runtimeName string, build bool) error {
	runtime, err := devcontainer.ParseRuntime(runtimeName)
	if err != nil {
		return err
	}
	if !devcontainer.ProjectHasConfig(project) {
		return fmt.Errorf("project %s has no .devcontainer; run 'ai-agent up --workspace %s' to use the generic image instead", project, project)
	}
	overlay, err := l.Overlay.Args(project)
	if err != nil {
		return err
	}
	l.report(Progress{Kind: ProjectLaunching, Target: project, Runtime: runtimeName})
	if err := l.Runner(ctx, devcontainerBin, devcontainer.UpArgs(runtime, project, overlay, build), l.commandStreams()); err != nil {
		return fmt.Errorf("devcontainer up: %w", err)
	}
	bootstrap := devcontainer.ProjectExecArgs(runtime, project, overlay, path.Join(devcontainer.ContainerBinDir, "ai-agent"), "bootstrap", "--quiet")
	if err := l.Runner(ctx, devcontainerBin, bootstrap, l.commandStreams()); err != nil {
		l.report(Progress{Kind: ProjectBootstrapFailed, Err: fmt.Errorf("bootstrap project devcontainer: %w", err)})
	}
	command := devcontainer.ExecShellCommand(project, runtime, overlay)
	l.report(Progress{Kind: ProjectReady, Command: command})
	l.runAuthStatus(ctx, devcontainerBin, devcontainer.ProjectExecArgs(runtime, project, overlay, path.Join(devcontainer.ContainerBinDir, "ai-agent"), "auth", "status"))
	l.report(Progress{Kind: ShellOpening})
	args := devcontainer.ProjectExecArgs(runtime, project, overlay, "sh", "-c", devcontainer.FallbackShell)
	if err := l.Runner(ctx, devcontainerBin, args, l.Streams); err != nil {
		return fmt.Errorf("open shell in devcontainer: %w (re-enter with: %s)", err, command)
	}
	return nil
}

func (l ContainerLauncher) runAuthStatus(ctx context.Context, devcontainerBin string, args []string) {
	l.report(Progress{Kind: AuthStatusChecking})
	if err := l.Runner(ctx, devcontainerBin, args, l.commandStreams()); err != nil {
		l.report(Progress{Kind: AuthStatusFailed, Err: fmt.Errorf("agent login status: %w", err)})
	}
}

func (l ContainerLauncher) commandStreams() Streams {
	return Streams{Out: l.CommandOut, Err: l.CommandErr}
}

func (l ContainerLauncher) report(progress Progress) {
	report(l.Progress, progress)
}
