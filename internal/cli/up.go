package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/app/readiness"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/governance"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/devcontainer"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/uphost"
	"github.com/spf13/cobra"
)

type upOptions struct {
	workspace        string
	project          string
	command          []string
	build            bool
	observability    bool
	runtime          string
	verbose          bool
	embedded         bool
	containerStarted func(context.Context, string, string) error
}

func newUpCommand(services ProviderServices) *cobra.Command {
	options := upOptions{workspace: ".", runtime: string(containerRuntimePodman), observability: true}
	command := &cobra.Command{
		Use:   "up",
		Short: "Bootstrap the full local dev environment in one command",
		Long: `Ensures the broker is running, validates host readiness, builds (if needed)
and launches the devcontainer, then opens an interactive shell inside it.

This is the compatibility entrypoint for an explicitly mounted workspace.
In the generic devcontainer, agent CLI login state persists in the ai-agent-home
volume mounted at /home/dev, while GitHub repo credentials remain brokered
through ai-agent run.

Examples:
  ai-agent up
  ai-agent up --workspace ~/github
  ai-agent up --project ~/github/my-rails-app
  ai-agent up --build
  ai-agent up --observability=false`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	command.RunE = func(command *cobra.Command, args []string) error { return runUp(command, options, services) }
	command.Flags().StringVar(&options.workspace, "workspace", options.workspace, "path to the workspace directory to mount")
	command.Flags().StringVar(&options.project, "project", "", "path to a single project whose own .devcontainer should be honored, with the broker overlay injected")
	bindContainerFlags(command, &options.runtime, &options.build, &options.observability, &options.verbose)
	return command
}

func bindContainerFlags(command *cobra.Command, runtime *string, build, observability, verbose *bool) {
	command.Flags().StringVar(runtime, "runtime", *runtime, "container runtime to use: podman or docker")
	command.Flags().BoolVar(build, "build", *build, "force rebuild of the managed devcontainer image")
	command.Flags().BoolVar(observability, "observability", *observability, "start local Langfuse observability")
	command.Flags().BoolVarP(verbose, "verbose", "v", *verbose, "stream container build output to the terminal instead of the log")
}

type upCLIAdapter struct {
	command     *cobra.Command
	scanner     *bufio.Scanner
	stdin       io.Reader
	lookPath    func(string) (string, error)
	runCommand  func(*exec.Cmd) error
	guidedSetup func(*bufio.Scanner) error
	install     func(containerRuntime, *bufio.Scanner) (containerRuntime, bool)
	readiness   readiness.Service
}

func newUpCLIAdapter(command *cobra.Command, services ProviderServices) *upCLIAdapter {
	adapter := &upCLIAdapter{
		command:    command,
		stdin:      command.InOrStdin(),
		lookPath:   exec.LookPath,
		runCommand: func(process *exec.Cmd) error { return process.Run() },
		readiness:  newReadinessService(services.ValidatePolicy),
	}
	adapter.scanner = bufio.NewScanner(adapter.stdin)
	adapter.guidedSetup = func(scanner *bufio.Scanner) error {
		return runSetupWithNext(command, scanner, "continuing: starting broker and devcontainer", services, setupOptions{})
	}
	adapter.install = adapter.installMissing
	return adapter
}

func runUp(cmd *cobra.Command, options upOptions, services ProviderServices) error {
	_, err := runUpContext(commandContext(cmd), cmd, options, services)
	return err
}

func runUpContext(ctx context.Context, cmd *cobra.Command, options upOptions, services ProviderServices) (bool, error) {
	runtime, err := parseContainerRuntime(options.runtime)
	if err != nil {
		return true, err
	}
	adapter := newUpCLIAdapter(cmd, services)
	reporter, err := newUpReporter(cmd, options.verbose)
	if err != nil {
		return true, err
	}
	defer reporter.Close()
	streams := uphost.Streams{In: cmd.InOrStdin(), Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()}
	container := uphost.NewContainerLauncher(streams, reporter.progressFunc())
	container.Overlay = devcontainer.NewOverlayBuilder(os.Executable)
	container.CommandOut = reporter.commandWriter()
	container.CommandErr = reporter.commandWriter()
	workspace, err := uphost.PrepareWorkspace(options.workspace, options.project)
	if err != nil {
		return true, fmt.Errorf("resolve workspace: %w", err)
	}
	runtime, err = adapter.EnsureHost(runtime)
	if err != nil {
		return true, err
	}
	if !options.embedded {
		if err := adapter.EnsureConfigured(); err != nil {
			return true, err
		}
	}
	if options.observability {
		obsStreams := uphost.Streams{Out: reporter.commandWriter(), Err: reporter.commandWriter()}
		if err := uphost.StartObservability(ctx, obsStreams, reporter.progressFunc(), services.ValidatePolicy); err != nil {
			return true, upFailure(reporter, options.embedded, "Langfuse startup failed", err)
		}
	}
	brokerSocketPath, err := paths.BrokerListenSocketPath()
	if err != nil {
		return true, err
	}
	if err := uphost.EnsureBroker(ctx, brokerSocketPath, cmd.ErrOrStderr(), resolveOptionalBinary); err != nil {
		return true, fmt.Errorf("broker startup: %w", err)
	}
	runtime, err = adapter.EnsureManaged(runtime)
	if err != nil {
		return true, err
	}
	devcontainerBin, err := container.FindCLI()
	if err != nil {
		return true, fmt.Errorf("devcontainer CLI not found in PATH: %w", err)
	}
	if options.project != "" {
		return true, upFailure(reporter, options.embedded, "Project devcontainer launch failed", container.LaunchProject(ctx, devcontainerBin, workspace, string(runtime), options.build))
	}
	target, err := container.PrepareGenericRoot(workspace)
	if err != nil {
		return true, fmt.Errorf("prepare devcontainer: %w", err)
	}
	if len(options.command) > 0 {
		if options.embedded {
			quiesced, launchErr := container.LaunchEphemeralGenericCommand(ctx, devcontainerBin, workspace, target, string(runtime), options.build, options.command, options.containerStarted)
			if quiesced {
				launchErr = errors.Join(launchErr, devcontainer.RemoveGenericRoot(paths.DataDir(), workspace))
			}
			return quiesced, launchErr
		}
		err := container.LaunchGenericCommand(ctx, devcontainerBin, workspace, target, string(runtime), options.build, options.command)
		return true, reporter.fail("Governed session failed", err)
	}
	return true, reporter.fail("Devcontainer launch failed", container.LaunchGeneric(ctx, devcontainerBin, workspace, target, string(runtime), options.build))
}

func upFailure(reporter *upReporter, embedded bool, message string, err error) error {
	if embedded {
		return err
	}
	return reporter.fail(message, err)
}

func (a *upCLIAdapter) EnsureHost(runtime containerRuntime) (containerRuntime, error) {
	report := buildUpHostReadinessReport(a.readiness, runtime)
	if report.Ready {
		return runtime, nil
	}

	var fixed bool
	runtime, fixed = a.tryAutoFix(report, runtime, a.scanner)
	if fixed {
		report = buildUpHostReadinessReport(a.readiness, runtime)
	}
	if !report.Ready {
		writeDoctorText(a.command.OutOrStdout(), report)
		return runtime, fmt.Errorf("host readiness checks failed; fix the issues above before running guided setup")
	}
	return runtime, nil
}

func buildUpHostReadinessReport(service readiness.Service, runtime containerRuntime) readiness.Report {
	runtimeDir := paths.RuntimeBaseDir()
	source := "fallback"
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		source = "XDG_RUNTIME_DIR"
	}
	checks := []readiness.Check{service.RuntimeDir(runtimeDir, source)}
	checks = append(checks, service.Binaries(true)...)
	checks = append(checks, service.Workspace(os.Getenv(paths.EnvWorkspace)))
	checks = append(checks, service.ContainerRuntime(string(runtime)))
	socketPath, err := paths.BrokerListenSocketPath()
	if err != nil {
		checks = append(checks, readiness.Check{
			Name:        "broker-socket-env",
			Status:      readiness.StatusFail,
			Details:     err.Error(),
			Remediation: "Point " + paths.EnvBrokerSocket + " at an absolute socket path or unset it to use the runtime-directory default.",
		})
	}
	readiness.Classify(checks)
	return readiness.Report{
		Mode:       readiness.ModeUp,
		Ready:      !readiness.HasFailure(checks),
		Outcome:    readiness.Outcome(checks),
		RuntimeDir: runtimeDir,
		SocketPath: socketPath,
		Checks:     checks,
	}
}

func (a *upCLIAdapter) EnsureManaged(runtime containerRuntime) (containerRuntime, error) {
	socketPath, err := paths.BrokerListenSocketPath()
	if err != nil {
		return runtime, err
	}
	report := a.readiness.Run(readinessInput(readiness.ModeUp, socketPath, "", runtime))
	if !report.Ready {
		var fixed bool
		runtime, fixed = a.tryAutoFix(report, runtime, a.scanner)
		if fixed {
			report = a.readiness.Run(readinessInput(readiness.ModeUp, socketPath, "", runtime))
		}
		if !report.Ready {
			writeDoctorText(a.command.OutOrStdout(), report)
			return runtime, fmt.Errorf("readiness checks failed; fix the issues above before running 'ai-agent up'")
		}
	}
	if report.Outcome == readiness.StatusWarn {
		_, _ = fmt.Fprintln(a.command.OutOrStdout(), "doctor: checks passed with advisories (see notes above)")
	} else {
		_, _ = fmt.Fprintln(a.command.OutOrStdout(), "doctor: all checks passed")
	}
	return runtime, nil
}

func (a *upCLIAdapter) EnsureConfigured() error {
	issues := firstUseConfigIssues(a.readiness)
	if len(issues) == 0 {
		return nil
	}

	w := a.command.OutOrStdout()
	_, _ = fmt.Fprintf(w, "first-time configuration needs attention: %s\n", strings.Join(issues, "; "))
	_, _ = fmt.Fprintln(w, "guided setup needs a GitHub App that is already installed on your target repos, its App ID, and the downloaded PEM private key path")
	if !promptYNWithScanner(w, a.scanner, "Run guided setup now?") {
		return fmt.Errorf("first-time configuration is required before 'ai-agent up'; run 'ai-agent setup' or rerun 'ai-agent up' and accept guided setup")
	}

	if err := a.guidedSetup(a.scanner); err != nil {
		return fmt.Errorf("guided setup: %w", err)
	}
	return nil
}

func firstUseConfigIssues(service readiness.Service) []string {
	governancePaths := governance.DefaultPaths()
	issues := make([]string, 0)
	for _, check := range service.Configuration(governancePaths.Identities, governancePaths.Policy) {
		if check.Status == readiness.StatusFail {
			issues = append(issues, check.Details)
		}
	}
	return issues
}

func (a *upCLIAdapter) tryAutoFix(report readiness.Report, runtime containerRuntime, scanner *bufio.Scanner) (containerRuntime, bool) {
	for _, check := range report.Checks {
		if check.Name == "container-runtime" && check.Status == readiness.StatusFail {
			return a.install(runtime, scanner)
		}
	}
	return runtime, false
}

func (a *upCLIAdapter) installMissing(runtime containerRuntime, scanner *bufio.Scanner) (containerRuntime, bool) {
	fixed := false
	selectedRuntime := runtime

	if _, err := a.lookPath(string(runtime)); err != nil && runtime == containerRuntimePodman {
		if _, dockerErr := a.lookPath(string(containerRuntimeDocker)); dockerErr == nil {
			switch promptPodmanFallbackWithScanner(a.command.OutOrStdout(), scanner) {
			case "install":
				if err := a.installPodman(); err == nil {
					fixed = true
				}
			case "docker":
				selectedRuntime = containerRuntimeDocker
				fixed = true
				_, _ = fmt.Fprintln(a.command.OutOrStdout(), "using docker for this run; pass --runtime docker next time to opt out explicitly")
			}
		} else if promptYNWithScanner(a.command.OutOrStdout(), scanner, "Selected runtime podman is not installed. Install Podman now?") {
			if err := a.installPodman(); err == nil {
				fixed = true
			}
		}
	}

	if _, err := a.lookPath("devcontainer"); err != nil {
		if promptYNWithScanner(a.command.OutOrStdout(), scanner, "devcontainer CLI is not installed. Install it now?") {
			if err := a.installDevcontainer(); err == nil {
				fixed = true
			}
		}
	}

	return selectedRuntime, fixed
}

func promptYNWithScanner(w io.Writer, scanner *bufio.Scanner, question string) bool {
	_, _ = fmt.Fprintf(w, "%s [y/N] ", question)
	if !scanner.Scan() {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(scanner.Text()), "y")
}

func promptPodmanFallbackWithScanner(w io.Writer, scanner *bufio.Scanner) string {
	_, _ = fmt.Fprint(w, "Selected runtime podman is not installed, but docker is available. Choose: [i] install Podman and continue, [d] use Docker for this run, [N] cancel ")
	if !scanner.Scan() {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
	case "i", "install", "podman":
		return "install"
	case "d", "docker":
		return "docker"
	default:
		return ""
	}
}

func (a *upCLIAdapter) installPodman() error {
	_, _ = fmt.Fprintln(a.command.OutOrStdout(), "installing podman via apt-get...")
	c := exec.Command("sudo", "apt-get", "install", "-y", "podman")
	c.Stdin = a.stdin
	c.Stdout = a.command.OutOrStdout()
	c.Stderr = a.command.OutOrStderr()
	if err := a.runCommand(c); err != nil {
		_, _ = fmt.Fprintf(a.command.OutOrStderr(), "failed to install podman: %v\n", err)
		return err
	}
	_, _ = fmt.Fprintln(a.command.OutOrStdout(), "podman installed successfully")
	return nil
}

func (a *upCLIAdapter) installDevcontainer() error {
	npmBin, err := a.lookPath("npm")
	if err != nil {
		_, _ = fmt.Fprintln(a.command.OutOrStderr(), "npm not found in PATH; install Node.js first, then run: npm install -g @devcontainers/cli")
		return err
	}
	_, _ = fmt.Fprintln(a.command.OutOrStdout(), "installing devcontainer CLI via npm...")
	c := exec.Command(npmBin, "install", "-g", "@devcontainers/cli")
	c.Stdout = a.command.OutOrStdout()
	c.Stderr = a.command.OutOrStderr()
	if err := a.runCommand(c); err != nil {
		_, _ = fmt.Fprintf(a.command.OutOrStderr(), "failed to install devcontainer CLI: %v\n", err)
		return err
	}
	_, _ = fmt.Fprintln(a.command.OutOrStdout(), "devcontainer CLI installed successfully")
	return nil
}
