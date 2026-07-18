package uphost

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/devcontainer"
)

type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type CommandRunner func(context.Context, string, []string, Streams) error

func runCommand(ctx context.Context, name string, args []string, streams Streams) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = streams.In
	command.Stdout = streams.Out
	command.Stderr = streams.Err
	return command.Run()
}

type ContainerLauncher struct {
	Streams     Streams
	Progress    ProgressFunc
	Runner      CommandRunner
	LookPath    func(string) (string, error)
	PrepareRoot func(workspace string) (string, error)
	Overlay     devcontainer.OverlayBuilder
}

func NewContainerLauncher(streams Streams, progress ProgressFunc) ContainerLauncher {
	return ContainerLauncher{
		Streams:  streams,
		Progress: progress,
		Runner:   runCommand,
		LookPath: exec.LookPath,
		PrepareRoot: func(workspace string) (string, error) {
			return devcontainer.PrepareGenericRoot(paths.DataDir(), workspace, os.Executable)
		},
		Overlay: devcontainer.NewOverlayBuilder(os.Executable),
	}
}

func (l ContainerLauncher) FindCLI() (string, error) {
	return l.LookPath("devcontainer")
}

func (l ContainerLauncher) PrepareGenericRoot(workspace string) (string, error) {
	return l.PrepareRoot(workspace)
}

func (l ContainerLauncher) LaunchGeneric(ctx context.Context, devcontainerBin, workspace, target, runtimeName string, build bool) error {
	runtime, err := devcontainer.ParseRuntime(runtimeName)
	if err != nil {
		return err
	}
	l.report(Progress{Kind: GenericLaunching, Target: target, Runtime: runtimeName})
	if err := l.Runner(ctx, devcontainerBin, devcontainer.UpArgs(runtime, target, nil, build), Streams{Out: l.Streams.Out, Err: l.Streams.Err}); err != nil {
		return fmt.Errorf("devcontainer up: %w", err)
	}
	l.report(Progress{Kind: GenericReady, Target: target, Workspace: workspace, Runtime: runtimeName, Command: devcontainer.ExecCommand(target, runtime)})
	l.runAuthStatus(ctx, devcontainerBin, devcontainer.ProjectExecArgs(runtime, target, nil, "ai-agent", "auth", "status"))
	l.report(Progress{Kind: ShellOpening})
	args := append([]string{"exec"}, devcontainer.RuntimeArgs(runtime)...)
	args = append(args, "--workspace-folder", target, "bash")
	if err := l.Runner(ctx, devcontainerBin, args, l.Streams); err != nil {
		return fmt.Errorf("open shell in devcontainer: %w (re-enter with: %s)", err, devcontainer.ExecCommand(target, runtime))
	}
	return nil
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
	if err := l.Runner(ctx, devcontainerBin, devcontainer.UpArgs(runtime, project, overlay, build), Streams{Out: l.Streams.Out, Err: l.Streams.Err}); err != nil {
		return fmt.Errorf("devcontainer up: %w", err)
	}
	bootstrap := devcontainer.ProjectExecArgs(runtime, project, overlay, path.Join(devcontainer.ContainerBinDir, "ai-agent"), "bootstrap", "--quiet")
	if err := l.Runner(ctx, devcontainerBin, bootstrap, Streams{Out: l.Streams.Out, Err: l.Streams.Err}); err != nil {
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
	if err := l.Runner(ctx, devcontainerBin, args, Streams{Out: l.Streams.Out, Err: l.Streams.Err}); err != nil {
		l.report(Progress{Kind: AuthStatusFailed, Err: fmt.Errorf("agent login status: %w", err)})
	}
}

func (l ContainerLauncher) report(progress Progress) {
	report(l.Progress, progress)
}
