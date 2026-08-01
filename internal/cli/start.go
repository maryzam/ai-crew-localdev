package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/agents/capabilities"
	"github.com/maryzam/ai-crew-localdev/internal/app/startsession"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/governance"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/workspace"
	"github.com/spf13/cobra"
)

type startOptions struct {
	agent    string
	runtime  string
	build    bool
	langfuse bool
	new      bool
}

type startUseCase interface {
	Start(context.Context, startsession.Request) (startsession.Result, error)
}

type startUseCaseFactory func(*cobra.Command, startOptions) startUseCase

func newStartCommand(services ProviderServices) *cobra.Command {
	return newStartCommandWithFactory(services, func(command *cobra.Command, options startOptions) startUseCase {
		return defaultStartUseCase(command, options, services)
	})
}

func newStartCommandWithFactory(services ProviderServices, factory startUseCaseFactory) *cobra.Command {
	options := startOptions{runtime: string(containerRuntimePodman)}
	command := &cobra.Command{
		Use:   "start [repository] -- [agent-arguments...]",
		Short: "Start a governed agent in a private repository workspace",
		Long: `Creates or resumes an isolated local checkout for one repository, mounts only
that checkout at /workspace, and starts the configured agent through the broker.
The source checkout is unchanged until 'ai-agent apply' succeeds.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	command.RunE = func(command *cobra.Command, args []string) error {
		source, agentArgs, err := parseStartArguments(command, args)
		if err != nil {
			renderCLIError(command, "Start", err)
			return err
		}
		_, _ = fmt.Fprintln(command.OutOrStdout(), "Preparing private repository workspace")
		result, startErr := factory(command, options).Start(commandContext(command), startsession.Request{
			SourcePath: source,
			AgentName:  options.agent,
			AgentArgs:  agentArgs,
			New:        options.new,
		})
		renderStartResult(command, result)
		if startErr != nil {
			renderCLIError(command, "Start", startErr)
		}
		return startErr
	}
	command.Flags().StringVar(&options.agent, "agent", "", "configured agent identity; required when more than one is configured")
	command.Flags().StringVar(&options.runtime, "runtime", options.runtime, "container runtime to use: podman or docker")
	command.Flags().BoolVar(&options.build, "build", false, "force rebuild of the managed devcontainer image")
	command.Flags().BoolVar(&options.langfuse, "langfuse", false, "start Langfuse observability as a sidecar")
	command.Flags().BoolVar(&options.new, "new", false, "create another private workspace instead of resuming the active one")
	command.SetFlagErrorFunc(func(command *cobra.Command, err error) error {
		renderCLIError(command, "Start", err)
		return err
	})
	return command
}

func parseStartArguments(command *cobra.Command, args []string) (string, []string, error) {
	dash := command.ArgsLenAtDash()
	positionals := args
	var agentArgs []string
	if dash >= 0 {
		positionals = args[:dash]
		agentArgs = append([]string(nil), args[dash:]...)
	}
	if len(positionals) > 1 {
		return "", nil, fmt.Errorf("start accepts at most one repository path before --")
	}
	source := "."
	if len(positionals) == 1 {
		source = positionals[0]
	}
	return source, agentArgs, nil
}

func renderStartResult(command *cobra.Command, result startsession.Result) {
	if result.Workspace.ID == "" {
		return
	}
	if result.WorkspaceResult.ResultCommit == "" {
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Workspace %s retained for recovery\n", result.Workspace.ID)
		return
	}
	if result.WorkspaceResult.Changed {
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Workspace %s preserved result %s\n", result.Workspace.ID, result.WorkspaceResult.ResultCommit)
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Apply it from the source repository with: ai-agent apply --workspace %s\n", result.Workspace.ID)
		return
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Workspace %s completed without repository changes\n", result.Workspace.ID)
}

func defaultStartUseCase(command *cobra.Command, options startOptions, services ProviderServices) startUseCase {
	manager := workspace.NewManager(paths.DataDir())
	manager.Observer = workspaceObserver(command)
	workspacePort := &startWorkspacePort{manager: manager, prepared: make(map[string]workspace.Workspace), leases: make(map[string]*workspace.RunLease)}
	agents := startAgentConfiguration{command: command, services: services}
	launcher := startContainerPort{command: command, services: services, options: options}
	return startsession.New(agents, workspacePort, launcher)
}

type startAgentConfiguration struct {
	command  *cobra.Command
	services ProviderServices
}

func (configuration startAgentConfiguration) ConfiguredAgents(context.Context) ([]startsession.Agent, error) {
	adapter := newUpCLIAdapter(configuration.command, configuration.services)
	if err := adapter.EnsureConfigured(); err != nil {
		return nil, err
	}
	snapshot, err := (governance.FileStore{}).Load(governance.DefaultPaths())
	if err != nil {
		return nil, err
	}
	if snapshot.IdentitiesError != nil {
		return nil, snapshot.IdentitiesError
	}
	if snapshot.Identities == nil {
		return nil, fmt.Errorf("identities configuration is missing")
	}
	return configuredStartAgents(snapshot.Identities)
}

func configuredStartAgents(identities *identity.IdentitiesFile) ([]startsession.Agent, error) {
	if identities == nil {
		return nil, fmt.Errorf("identities configuration is missing")
	}
	names := make([]string, 0, len(identities.Agents))
	for name := range identities.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	agents := make([]startsession.Agent, 0, len(names))
	for _, name := range names {
		configured := identities.Agents[name]
		tool, err := compiledStartTool(name, configured.Tool)
		if err != nil {
			return nil, err
		}
		agents = append(agents, startsession.Agent{Name: name, Tool: tool, GitName: configured.GitName, GitEmail: configured.GitEmail})
	}
	return agents, nil
}

func compiledStartTool(agentName, configuredTool string) (string, error) {
	expectedTool := capabilities.DefaultToolForAgent(agentName)
	tool := strings.TrimSpace(configuredTool)
	if tool == "" {
		if expectedTool == "" {
			return "", fmt.Errorf("agent %q must configure a supported tool explicitly", agentName)
		}
		tool = expectedTool
	}
	if expectedTool != "" && !capabilities.CommandMatchesTool(tool, expectedTool) {
		return "", fmt.Errorf("agent %q configures unsupported tool %q", agentName, tool)
	}
	entry, ok := capabilities.FindByCommand(tool)
	if !ok || entry.Name == "" {
		return "", fmt.Errorf("agent %q configures unsupported tool %q", agentName, tool)
	}
	return entry.Name, nil
}

type startWorkspacePort struct {
	manager  workspace.Manager
	prepared map[string]workspace.Workspace
	leases   map[string]*workspace.RunLease
}

func (port *startWorkspacePort) Preflight(ctx context.Context, request startsession.WorkspaceRequest) error {
	return port.manager.Preflight(ctx, workspace.PrepareRequest{SourcePath: request.SourcePath, New: request.New})
}

func (port *startWorkspacePort) PrepareOrResume(ctx context.Context, request startsession.WorkspaceRequest) (startsession.Workspace, error) {
	prepared, err := port.manager.Prepare(ctx, workspace.PrepareRequest{SourcePath: request.SourcePath, New: request.New, AgentName: request.AgentName, Tool: request.Tool})
	if err != nil {
		return startsession.Workspace{}, err
	}
	port.prepared[prepared.ID] = prepared
	return startsession.Workspace{ID: prepared.ID, SourceRoot: prepared.SourceRoot, CheckoutPath: prepared.CheckoutPath, BaseCommit: prepared.BaseCommit}, nil
}

func (port *startWorkspacePort) Acquire(ctx context.Context, selected startsession.Workspace) (startsession.Lease, error) {
	prepared, ok := port.prepared[selected.ID]
	if !ok {
		return startsession.Lease{}, fmt.Errorf("workspace %s was not prepared by this start", selected.ID)
	}
	lease, err := port.manager.Acquire(ctx, prepared)
	if err != nil {
		return startsession.Lease{}, err
	}
	port.leases[selected.ID] = lease
	return startsession.Lease{ID: lease.ID}, nil
}

func (port *startWorkspacePort) Finalize(ctx context.Context, request startsession.FinalizeRequest) (startsession.WorkspaceResult, error) {
	prepared, ok := port.prepared[request.Workspace.ID]
	if !ok {
		return startsession.WorkspaceResult{}, fmt.Errorf("workspace %s was not prepared by this start", request.Workspace.ID)
	}
	lease, ok := port.leases[request.Workspace.ID]
	if !ok || lease.ID != request.Lease.ID {
		return startsession.WorkspaceResult{}, fmt.Errorf("workspace %s lease does not match this start", request.Workspace.ID)
	}
	delete(port.leases, request.Workspace.ID)
	result, err := port.manager.Complete(ctx, prepared, lease, workspace.Author{Name: request.GitName, Email: request.GitEmail}, workspace.RunOutcome(request.Outcome))
	if err != nil {
		return startsession.WorkspaceResult{}, err
	}
	return startsession.WorkspaceResult{ResultCommit: result.ResultCommit, Changed: result.ResultCommit != result.BaseCommit}, nil
}

type startContainerPort struct {
	command  *cobra.Command
	services ProviderServices
	options  startOptions
}

func (port startContainerPort) Launch(_ context.Context, request startsession.LaunchRequest) error {
	return runUp(port.command, upOptions{
		workspace: request.CheckoutPath,
		command:   append([]string(nil), request.Argv...),
		runtime:   port.options.runtime,
		build:     port.options.build,
		langfuse:  port.options.langfuse,
	}, port.services)
}
