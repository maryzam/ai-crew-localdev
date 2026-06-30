package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/maryzam/ai-crew-localdev/internal/brokerclient"
	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/configstore"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/launcher"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/maryzam/ai-crew-localdev/internal/readiness"
	"github.com/spf13/cobra"
)

type doctorOptions struct {
	brokerSocket string
	mode         string
	repository   string
	runtime      string
	json         bool
}

func newDoctorCommand(service readiness.Service) *cobra.Command {
	options := doctorOptions{}
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Validate host and devcontainer readiness",
		Long: `Validates the local prerequisites required for brokered auth sessions.

Run with --mode host to check host-native sessions, or --mode container to
check the stricter prerequisites needed before launching the devcontainer.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	command.Flags().StringVar(&options.mode, "mode", string(readiness.ModeHost), "readiness mode: host or container")
	command.Flags().StringVar(&options.brokerSocket, "broker-sock", "", "broker socket path (default: auto)")
	command.Flags().StringVar(&options.repository, "repo", "", "path to a git repository to validate (default: current directory when inside a repo)")
	command.Flags().StringVar(&options.runtime, "runtime", string(containerRuntimePodman), "container runtime to validate in container mode: podman or docker")
	command.Flags().BoolVar(&options.json, "json", false, "emit machine-readable JSON output")
	command.RunE = func(command *cobra.Command, args []string) error {
		return runDoctor(command, options, service)
	}
	return command
}

func runDoctor(cmd *cobra.Command, options doctorOptions, service readiness.Service) error {
	mode := readiness.Mode(options.mode)
	if mode != readiness.ModeHost && mode != readiness.ModeContainer {
		return fmt.Errorf("invalid --mode %q: expected host or container", options.mode)
	}
	runtime, err := parseContainerRuntime(options.runtime)
	if err != nil {
		return err
	}
	socketPath, err := resolveBrokerSocketPath(options.brokerSocket)
	if err != nil {
		return err
	}
	report := service.Run(readinessInput(mode, socketPath, options.repository, runtime))
	if options.json {
		if err := writeDoctorJSON(cmd.OutOrStdout(), report); err != nil {
			return err
		}
	} else {
		writeDoctorText(cmd.OutOrStdout(), report)
	}
	if report.Ready {
		return nil
	}
	return fmt.Errorf("readiness checks failed")
}

func newReadinessService(validator func(*policy.PolicyFile, *identity.IdentitiesFile) error) readiness.Service {
	service, err := readiness.New(readiness.Ports{Host: readinessHost{}, Binaries: readinessBinaries{}, Broker: readinessBroker{}, Repository: readinessRepository{}, Governance: readinessGovernance{}, Policy: readinessPolicy{validate: validator}})
	if err != nil {
		panic(err)
	}
	return service
}

type readinessHost struct{}

func (readinessHost) Stat(path string) (os.FileInfo, error)  { return os.Stat(path) }
func (readinessHost) Lstat(path string) (os.FileInfo, error) { return os.Lstat(path) }
func (readinessHost) WorkingDir() (string, error)            { return os.Getwd() }
func (readinessHost) Executable() (string, error)            { return os.Executable() }
func (readinessHost) ExpandPath(path string) string          { return config.ExpandHome(path) }
func (readinessHost) CanOpen(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return file.Close()
}

type readinessBinaries struct{}

func (readinessBinaries) Find(name string) (string, error) { return exec.LookPath(name) }

type readinessBroker struct{}

func (readinessBroker) Check(path string) error { return brokerHealthCheck(path) }

type readinessRepository struct{}

func (readinessRepository) Resolve(path string) (string, string, bool, error) {
	return launcher.ResolveRepo(path)
}

type readinessGovernance struct{}

func (readinessGovernance) Inspect(identitiesPath, policyPath string) (readiness.Configuration, error) {
	snapshot, err := configstore.Inspect(identitiesPath, policyPath)
	return readiness.Configuration{Identities: snapshot.Identities, Policy: snapshot.Policy, IdentitiesError: snapshot.IdentitiesError, PolicyError: snapshot.PolicyError}, err
}

type readinessPolicy struct {
	validate func(*policy.PolicyFile, *identity.IdentitiesFile) error
}

func (port readinessPolicy) Validate(policyFile *policy.PolicyFile, identities *identity.IdentitiesFile) error {
	return port.validate(policyFile, identities)
}

func readinessInput(mode readiness.Mode, socketPath, repoPath string, runtime containerRuntime) readiness.Input {
	runtimeSource := "fallback"
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		runtimeSource = "XDG_RUNTIME_DIR"
	}
	return readiness.Input{
		Mode:             mode,
		RuntimeDir:       config.RuntimeBaseDir(),
		RuntimeSource:    runtimeSource,
		SocketPath:       socketPath,
		RepoPath:         repoPath,
		Workspace:        os.Getenv("AI_AGENT_WORKSPACE"),
		IdentitiesPath:   config.ExpandHome(config.DefaultIdentitiesPath()),
		PolicyPath:       configuredPolicyPath(),
		ContainerRuntime: runtime.binaryName(),
	}
}

func writeDoctorText(w io.Writer, report readiness.Report) {
	_, _ = fmt.Fprintf(w, "ai-agent doctor (%s)\n", report.Mode)
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(w, "[%s] %s: %s\n", string(check.Status), check.Name, check.Details)
		if check.Remediation != "" && check.Status != readiness.StatusPass {
			_, _ = fmt.Fprintf(w, "  fix: %s\n", check.Remediation)
		}
	}
	if report.Ready {
		_, _ = fmt.Fprintln(w, "ready: all blocking checks passed")
		return
	}
	_, _ = fmt.Fprintln(w, "not ready: fix the failing checks above")
}

func writeDoctorJSON(w io.Writer, report readiness.Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func brokerHealthCheck(socketPath string) error {
	response, err := (&brokerclient.Client{SocketPath: socketPath}).HealthCheck()
	if err != nil {
		return err
	}
	if !response.Healthy {
		return fmt.Errorf("broker responded unhealthy")
	}
	return nil
}
