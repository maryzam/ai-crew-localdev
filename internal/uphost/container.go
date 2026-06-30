package uphost

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"

	upapplication "github.com/maryzam/ai-crew-localdev/internal/application/up"
	"github.com/maryzam/ai-crew-localdev/internal/devcontainer"
)

type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type CommandRunner interface {
	Run(context.Context, string, []string, Streams) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, streams Streams) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = streams.In
	command.Stdout = streams.Out
	command.Stderr = streams.Err
	return command.Run()
}

type ContainerLauncher struct {
	Streams    Streams
	Progress   ProgressSink
	Runner     CommandRunner
	LookPath   func(string) (string, error)
	RootFinder devcontainer.RootFinder
	Overlay    devcontainer.OverlayBuilder
}

func NewContainerLauncher(streams Streams, progress ProgressSink) ContainerLauncher {
	return ContainerLauncher{Streams: streams, Progress: progress, Runner: ExecRunner{}, LookPath: exec.LookPath, RootFinder: devcontainer.NewRootFinder(), Overlay: devcontainer.NewOverlayBuilder(os.Executable)}
}

func (l ContainerLauncher) FindCLI(context.Context) (string, error) {
	return l.LookPath("devcontainer")
}

func (l ContainerLauncher) FindGenericRoot(context.Context) (string, error) {
	return l.RootFinder.Find()
}

func (l ContainerLauncher) LaunchGeneric(ctx context.Context, input upapplication.LaunchInput) error {
	runtime, err := devcontainer.ParseRuntime(input.Runtime)
	if err != nil {
		return err
	}
	l.report(Progress{Kind: GenericLaunching, Target: input.Target, Runtime: input.Runtime})
	if err := l.Runner.Run(ctx, input.DevcontainerBin, devcontainer.UpArgs(runtime, input.Target, nil, input.Build), Streams{Out: l.Streams.Out, Err: l.Streams.Err}); err != nil {
		return fmt.Errorf("devcontainer up: %w", err)
	}
	l.report(Progress{Kind: GenericReady, Target: input.Target, Workspace: input.Workspace, Runtime: input.Runtime, Command: devcontainer.ExecCommand(input.Target, runtime)})
	l.report(Progress{Kind: ShellOpening})
	args := append([]string{"exec"}, devcontainer.RuntimeArgs(runtime)...)
	args = append(args, "--workspace-folder", input.Target, "bash")
	if err := l.Runner.Run(ctx, input.DevcontainerBin, args, l.Streams); err != nil {
		return fmt.Errorf("open shell in devcontainer: %w (re-enter with: %s)", err, devcontainer.ExecCommand(input.Target, runtime))
	}
	return nil
}

func (l ContainerLauncher) LaunchProject(ctx context.Context, input upapplication.LaunchInput) error {
	runtime, err := devcontainer.ParseRuntime(input.Runtime)
	if err != nil {
		return err
	}
	if !devcontainer.ProjectHasConfig(input.Target) {
		return fmt.Errorf("project %s has no .devcontainer; run 'ai-agent up --workspace %s' to use the generic image instead", input.Target, input.Target)
	}
	overlay, err := l.Overlay.Args(input.Target)
	if err != nil {
		return err
	}
	l.report(Progress{Kind: ProjectLaunching, Target: input.Target, Runtime: input.Runtime})
	if err := l.Runner.Run(ctx, input.DevcontainerBin, devcontainer.UpArgs(runtime, input.Target, overlay, input.Build), Streams{Out: l.Streams.Out, Err: l.Streams.Err}); err != nil {
		return fmt.Errorf("devcontainer up: %w", err)
	}
	bootstrap := devcontainer.ProjectExecArgs(runtime, input.Target, overlay, path.Join(devcontainer.ContainerBinDir, "ai-agent"), "bootstrap", "--quiet")
	if err := l.Runner.Run(ctx, input.DevcontainerBin, bootstrap, Streams{Out: l.Streams.Out, Err: l.Streams.Err}); err != nil {
		l.report(Progress{Kind: ProjectBootstrapFailed, Err: fmt.Errorf("bootstrap project devcontainer: %w", err)})
	}
	command := devcontainer.ExecShellCommand(input.Target, runtime, overlay)
	l.report(Progress{Kind: ProjectReady, Command: command})
	l.report(Progress{Kind: ShellOpening})
	args := devcontainer.ProjectExecArgs(runtime, input.Target, overlay, "sh", "-c", devcontainer.FallbackShell)
	if err := l.Runner.Run(ctx, input.DevcontainerBin, args, l.Streams); err != nil {
		return fmt.Errorf("open shell in devcontainer: %w (re-enter with: %s)", err, command)
	}
	return nil
}

func (l ContainerLauncher) report(progress Progress) {
	if l.Progress != nil {
		l.Progress.Report(progress)
	}
}
