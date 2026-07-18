package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/app/readiness"
	"github.com/maryzam/ai-crew-localdev/internal/broker/client"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/governance"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/control"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
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
	return readiness.New(readiness.Dependencies{
		Stat:              os.Stat,
		Lstat:             os.Lstat,
		WorkingDir:        os.Getwd,
		Executable:        os.Executable,
		ExpandPath:        paths.ExpandHome,
		FindBinary:        exec.LookPath,
		CheckBroker:       brokerHealthCheck,
		ResolveRepo:       resolveReadinessRepository,
		LoadConfiguration: loadReadinessConfiguration,
		ValidatePolicy:    validator,
		Now:               time.Now,
	})
}

func resolveReadinessRepository(repoPath string) (string, string, bool, error) {
	repo, err := control.ResolveRepository(repoPath)
	if err != nil {
		return "", "", false, err
	}
	return repo.RootPath, repo.Slug, repo.SSH, nil
}

func loadReadinessConfiguration(identitiesPath, policyPath string) (governance.Snapshot, error) {
	return governance.FileStore{}.Load(governance.Paths{Identities: identitiesPath, Policy: policyPath})
}

func readinessInput(mode readiness.Mode, socketPath, repoPath string, runtime containerRuntime) readiness.Input {
	runtimeSource := "fallback"
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		runtimeSource = "XDG_RUNTIME_DIR"
	}
	governancePaths := governance.DefaultPaths()
	return readiness.Input{
		Mode:             mode,
		RuntimeDir:       paths.RuntimeBaseDir(),
		RuntimeSource:    runtimeSource,
		SocketPath:       socketPath,
		RepoPath:         repoPath,
		Workspace:        os.Getenv(paths.EnvWorkspace),
		IdentitiesPath:   governancePaths.Identities,
		PolicyPath:       governancePaths.Policy,
		ContainerRuntime: string(runtime),
	}
}

func writeDoctorText(w io.Writer, report readiness.Report) {
	_, _ = fmt.Fprintf(w, "ai-agent doctor (%s)\n", report.Mode)
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(w, "[%s] %s: %s\n", string(check.Status), check.Name, check.Details)
		if check.Remediation != "" && check.Status != readiness.StatusPass {
			label := "fix"
			if check.Status == readiness.StatusWarn {
				label = "note"
			}
			_, _ = fmt.Fprintf(w, "  %s: %s\n", label, check.Remediation)
		}
	}
	switch {
	case !report.Ready:
		_, _ = fmt.Fprintln(w, "not ready: fix the failing checks above")
	case report.Outcome == readiness.StatusWarn:
		_, _ = fmt.Fprintln(w, "ready: checks passed with advisories (see notes above)")
	default:
		_, _ = fmt.Fprintln(w, "ready: all checks passed")
	}
}

func writeDoctorJSON(w io.Writer, report readiness.Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func brokerHealthCheck(socketPath string) error {
	response, err := (&client.Client{SocketPath: socketPath}).HealthCheck()
	if err != nil {
		return err
	}
	if !response.Healthy {
		return fmt.Errorf("broker responded unhealthy")
	}
	return nil
}
