package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	upapplication "github.com/maryzam/ai-crew-localdev/internal/application/up"
	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/configstore"
	"github.com/maryzam/ai-crew-localdev/internal/devcontainer"
	"github.com/maryzam/ai-crew-localdev/internal/readiness"
	"github.com/maryzam/ai-crew-localdev/internal/uphost"
	"github.com/spf13/cobra"
)

type upOptions struct {
	workspace string
	project   string
	build     bool
	langfuse  bool
	runtime   string
}

func newUpCommand(services ProviderServices) *cobra.Command {
	options := upOptions{workspace: ".", runtime: string(containerRuntimePodman)}
	command := &cobra.Command{
		Use:   "up",
		Short: "Bootstrap the full local dev environment in one command",
		Long: `Ensures the broker is running, validates host readiness, builds (if needed)
and launches the devcontainer, then opens an interactive shell inside it.

This is the single supported entrypoint for the ai-agent local dev environment.
In the generic devcontainer, agent CLI login state persists in the ai-agent-home
volume mounted at /home/dev, while GitHub repo credentials remain brokered
through ai-agent run.

Examples:
  ai-agent up
  ai-agent up --workspace ~/github
  ai-agent up --project ~/github/my-rails-app
  ai-agent up --build
  ai-agent up --langfuse`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	command.RunE = func(command *cobra.Command, args []string) error {
		return runUp(command, options, services)
	}
	command.Flags().StringVar(&options.workspace, "workspace", options.workspace, "path to the workspace directory to mount")
	command.Flags().StringVar(&options.project, "project", "", "path to a single project whose own .devcontainer should be honored, with the broker overlay injected")
	command.Flags().BoolVar(&options.build, "build", false, "force rebuild of the devcontainer image")
	command.Flags().BoolVar(&options.langfuse, "langfuse", false, "start Langfuse observability stack as a sidecar")
	command.Flags().StringVar(&options.runtime, "runtime", options.runtime, "container runtime to use: podman or docker")
	return command
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
	runtime, err := parseContainerRuntime(options.runtime)
	if err != nil {
		return err
	}
	adapter := newUpCLIAdapter(cmd, services)
	streams := uphost.Streams{In: cmd.InOrStdin(), Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()}
	progress := uphost.ProgressFunc(func(value uphost.Progress) { renderUpProgress(cmd, value) })
	container := uphost.NewContainerLauncher(streams, progress)
	container.Overlay = devcontainer.NewOverlayBuilder(os.Executable)
	observability := uphost.NewObservabilityStarter(streams, progress, services.ValidatePolicy)
	broker := uphost.NewBrokerSupervisor(config.DefaultSocketPath(), cmd.ErrOrStderr(), resolveOptionalBinary)
	useCase, err := upapplication.New(upapplication.Ports{Workspace: uphost.Workspace{}, Readiness: adapter, Setup: adapter, Observability: observability, Broker: broker, Container: container})
	if err != nil {
		return err
	}
	_, err = useCase.Run(commandContext(cmd), upapplication.Input{Workspace: options.workspace, Project: options.project, Runtime: string(runtime), Build: options.build, Langfuse: options.langfuse})
	return err
}

func renderUpProgress(command *cobra.Command, progress uphost.Progress) {
	out := command.OutOrStdout()
	switch progress.Kind {
	case uphost.GenericLaunching:
		_, _ = fmt.Fprintf(out, "launching devcontainer in %s with %s\n", progress.Target, progress.Runtime)
	case uphost.GenericReady:
		_, _ = fmt.Fprintf(out, "devcontainer is ready; your host workspace %s is mounted at /workspace\n", progress.Workspace)
		_, _ = fmt.Fprintf(out, "runtime: %s\n", progress.Runtime)
		_, _ = fmt.Fprintf(out, "re-enter later with: %s\n", progress.Command)
		_, _ = fmt.Fprintf(out, "find the backing container with: %s ps --filter %q\n", progress.Runtime, "label=devcontainer.local_folder="+progress.Target)
		_, _ = fmt.Fprintln(out, "agent CLI login state: Claude and Codex store personal sign-in/config under /home/dev")
		_, _ = fmt.Fprintln(out, "persistence: /home/dev is the ai-agent-home volume and survives container re-entry/restart")
		_, _ = fmt.Fprintln(out, "security: run git and gh through 'ai-agent run'; do not run 'gh auth login' in this container")
	case uphost.ProjectLaunching:
		_, _ = fmt.Fprintf(out, "launching project devcontainer in %s with %s\n", progress.Target, progress.Runtime)
	case uphost.ProjectBootstrapFailed:
		_, _ = fmt.Fprintf(command.ErrOrStderr(), "warning: optional agent defaults were not installed: %v\n", progress.Err)
	case uphost.ProjectReady:
		_, _ = fmt.Fprintln(out, "project devcontainer ready; broker and ai-agent toolchain injected")
		_, _ = fmt.Fprintf(out, "re-enter later with: %s\n", progress.Command)
	case uphost.ShellOpening:
		_, _ = fmt.Fprintln(out, "opening shell in devcontainer")
	case uphost.LangfuseEnvironment:
		_, _ = fmt.Fprintln(out, "langfuse: created .env from .env.example (review and change secrets before production use)")
	case uphost.LangfuseStarting:
		_, _ = fmt.Fprintln(out, "langfuse: starting observability stack")
	case uphost.LangfuseReady:
		_, _ = fmt.Fprintln(out, "langfuse: stack ready at http://localhost:3000")
	}
}

func (a *upCLIAdapter) EnsureHost(_ context.Context, value string) (string, error) {
	runtime, err := parseContainerRuntime(value)
	if err != nil {
		return value, err
	}
	report := buildUpHostReadinessReport(a.readiness, runtime)
	if report.Ready {
		return string(runtime), nil
	}

	var fixed bool
	runtime, fixed = a.tryAutoFix(report, runtime, a.scanner)
	if fixed {
		report = buildUpHostReadinessReport(a.readiness, runtime)
	}
	if !report.Ready {
		writeDoctorText(a.command.OutOrStdout(), report)
		return string(runtime), fmt.Errorf("host readiness checks failed; fix the issues above before running guided setup")
	}
	return string(runtime), nil
}

func buildUpHostReadinessReport(service readiness.Service, runtime containerRuntime) doctorReport {
	runtimeDir := config.RuntimeBaseDir()
	checks := []doctorCheck{checkRuntimeDir(service, runtimeDir)}
	checks = append(checks, checkBinaryReadinessForUp(service)...)
	checks = append(checks, checkContainerWorkspace(service))
	checks = append(checks, checkContainerRuntime(service, runtime))
	return doctorReport{
		Mode:       doctorModeUp,
		Ready:      !hasBlockingFailure(checks),
		RuntimeDir: runtimeDir,
		SocketPath: config.DefaultSocketPath(),
		Checks:     checks,
	}
}

func (a *upCLIAdapter) EnsureManaged(_ context.Context, value string) (string, error) {
	runtime, err := parseContainerRuntime(value)
	if err != nil {
		return value, err
	}
	report := buildDoctorReport(a.readiness, doctorModeUp, config.DefaultSocketPath(), "", runtime)
	if !report.Ready {
		var fixed bool
		runtime, fixed = a.tryAutoFix(report, runtime, a.scanner)
		if fixed {
			report = buildDoctorReport(a.readiness, doctorModeUp, config.DefaultSocketPath(), "", runtime)
		}
		if !report.Ready {
			writeDoctorText(a.command.OutOrStdout(), report)
			return string(runtime), fmt.Errorf("readiness checks failed; fix the issues above before running 'ai-agent up'")
		}
	}
	_, _ = fmt.Fprintln(a.command.OutOrStdout(), "doctor: all checks passed")
	return string(runtime), nil
}

func (a *upCLIAdapter) EnsureConfigured(context.Context) error {
	issues, err := firstUseConfigIssues(a.readiness)
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		return nil
	}

	w := a.command.OutOrStdout()
	_, _ = fmt.Fprintf(w, "first-time configuration needs attention: %s\n", strings.Join(issues, "; "))
	if !promptYNWithScanner(w, a.scanner, "Run guided setup now?") {
		return fmt.Errorf("first-time configuration is required before 'ai-agent up'; run 'ai-agent setup' or rerun 'ai-agent up' and accept guided setup")
	}

	if err := a.guidedSetup(a.scanner); err != nil {
		return fmt.Errorf("guided setup: %w", err)
	}
	return nil
}

func firstUseConfigIssues(service readiness.Service) ([]string, error) {
	var issues []string

	identitiesPath := config.ExpandHome(config.DefaultIdentitiesPath())
	policyPath := configuredPolicyPath()
	snapshot, err := configstore.Inspect(identitiesPath, policyPath)
	if err != nil {
		return nil, fmt.Errorf("recover governance configuration: %w", err)
	}
	idents, identityCheck := loadedIdentitiesCheck(identitiesPath, snapshot.Identities, snapshot.IdentitiesError)
	if identityCheck.Status == doctorStatusFail {
		issues = append(issues, identityCheck.Details)
	}

	pol, policyCheck := loadedPolicyCheck(policyPath, snapshot.Policy, snapshot.PolicyError)
	if policyCheck.Status == doctorStatusFail {
		issues = append(issues, policyCheck.Details)
	}

	if idents != nil {
		for _, check := range checkIdentityKeys(service, *idents) {
			if check.Status == doctorStatusFail {
				issues = append(issues, check.Details)
			}
		}
	}

	if idents != nil && pol != nil {
		for _, check := range []doctorCheck{
			checkPolicyProviderConfig(service, idents, pol, policyPath),
			checkInstallationIDs(*idents, *pol, policyPath),
		} {
			if check.Status == doctorStatusFail {
				issues = append(issues, check.Details)
			}
		}
	}

	return issues, nil
}

func (a *upCLIAdapter) tryAutoFix(report doctorReport, runtime containerRuntime, scanner *bufio.Scanner) (containerRuntime, bool) {
	for _, check := range report.Checks {
		if check.Name == "container-runtime" && check.Status == doctorStatusFail {
			return a.install(runtime, scanner)
		}
	}
	return runtime, false
}

func (a *upCLIAdapter) installMissing(runtime containerRuntime, scanner *bufio.Scanner) (containerRuntime, bool) {
	fixed := false
	selectedRuntime := runtime

	if _, err := a.lookPath(runtime.binaryName()); err != nil && runtime == containerRuntimePodman {
		if _, dockerErr := a.lookPath(containerRuntimeDocker.binaryName()); dockerErr == nil {
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

func (a *upCLIAdapter) promptYN(w io.Writer, question string) bool {
	return promptYNWithScanner(w, bufio.NewScanner(a.stdin), question)
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
