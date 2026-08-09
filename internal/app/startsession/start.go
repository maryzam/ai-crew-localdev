package startsession

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const containerWorkspacePath = "/workspace"

type Agent struct {
	Name     string
	Tool     string
	GitName  string
	GitEmail string
}

type AgentConfiguration interface {
	ConfiguredAgents(context.Context) ([]Agent, error)
}

type WorkspaceRequest struct {
	SourcePath string
	New        bool
	AgentName  string
	Tool       string
}

type Workspace struct {
	ID                           string
	SourceRoot                   string
	CheckoutPath                 string
	BaseCommit                   string
	NeedsContainerReconciliation bool
	Container                    ContainerHandle
}

type WorkspaceResult struct {
	ResultCommit string
	Changed      bool
}

type WorkspaceManager interface {
	Plan(context.Context, WorkspaceRequest) (WorkspacePlan, error)
	PrepareOrResume(context.Context, WorkspacePlan, WorkspaceRequest) (Workspace, error)
	Reconcile(context.Context, Workspace, func(context.Context, ContainerHandle) error) (Workspace, error)
	Acquire(context.Context, Workspace) (Lease, error)
	RecordContainer(context.Context, Workspace, Lease, ContainerHandle) error
	Finalize(context.Context, FinalizeRequest) (WorkspaceResult, error)
}

type WorkspacePlan struct {
	ID string
}

type Lease struct {
	ID string
}

type ExecutionOutcome string

const (
	ExecutionSucceeded ExecutionOutcome = "succeeded"
	ExecutionFailed    ExecutionOutcome = "failed"
	ExecutionCanceled  ExecutionOutcome = "canceled"
)

type FinalizeRequest struct {
	Workspace  Workspace
	Lease      Lease
	GitName    string
	GitEmail   string
	Outcome    ExecutionOutcome
	Checkpoint bool
}

type LaunchRequest struct {
	WorkspaceID  string
	CheckoutPath string
	Argv         []string
}

type ContainerHandle struct {
	Runtime string
	ID      string
}

type ContainerLauncher interface {
	Launch(context.Context, LaunchRequest, func(context.Context, ContainerHandle) error) (LaunchResult, error)
	Reconcile(context.Context, LaunchRequest, ContainerHandle) error
}

type LaunchResult struct {
	WorkspaceQuiesced bool
}

type Request struct {
	SourcePath string
	AgentName  string
	AgentArgs  []string
	New        bool
}

type Result struct {
	Agent           Agent
	Workspace       Workspace
	WorkspaceResult WorkspaceResult
}

type UseCase struct {
	agents     AgentConfiguration
	workspaces WorkspaceManager
	launcher   ContainerLauncher
}

func New(agents AgentConfiguration, workspaces WorkspaceManager, launcher ContainerLauncher) *UseCase {
	return &UseCase{agents: agents, workspaces: workspaces, launcher: launcher}
}

func (useCase *UseCase) Start(ctx context.Context, request Request) (Result, error) {
	if useCase == nil || useCase.agents == nil || useCase.workspaces == nil || useCase.launcher == nil {
		return Result{}, fmt.Errorf("start session dependencies are not configured")
	}
	plan, err := useCase.workspaces.Plan(ctx, WorkspaceRequest{SourcePath: request.SourcePath, New: request.New})
	if err != nil {
		return Result{}, fmt.Errorf("validate source repository: %w", err)
	}

	agents, err := useCase.agents.ConfiguredAgents(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("load agent configuration: %w", err)
	}
	agent, err := selectAgent(agents, request.AgentName)
	if err != nil {
		return Result{}, err
	}

	workspace, err := useCase.workspaces.PrepareOrResume(ctx, plan, WorkspaceRequest{AgentName: agent.Name, Tool: agent.Tool})
	if err != nil {
		return Result{Agent: agent}, fmt.Errorf("prepare private workspace: %w", err)
	}
	result := Result{Agent: agent, Workspace: workspace}
	launchRequest := LaunchRequest{WorkspaceID: workspace.ID, CheckoutPath: workspace.CheckoutPath, Argv: agentArgv(agent, request.AgentArgs)}
	if workspace.NeedsContainerReconciliation {
		workspace, err = useCase.workspaces.Reconcile(context.WithoutCancel(ctx), workspace, func(reconcileCtx context.Context, handle ContainerHandle) error {
			return useCase.launcher.Reconcile(reconcileCtx, launchRequest, handle)
		})
		result.Workspace = workspace
		if err != nil {
			return result, fmt.Errorf("reconcile private workspace container: %w", err)
		}
	}
	lease, err := useCase.workspaces.Acquire(ctx, workspace)
	if err != nil {
		return result, fmt.Errorf("acquire private workspace lease: %w", err)
	}

	launchResult, launchErr := useCase.launcher.Launch(ctx, launchRequest, func(recordCtx context.Context, handle ContainerHandle) error {
		return useCase.workspaces.RecordContainer(context.WithoutCancel(recordCtx), workspace, lease, handle)
	})
	workspaceResult, finalizeErr := useCase.workspaces.Finalize(context.WithoutCancel(ctx), FinalizeRequest{
		Workspace:  workspace,
		Lease:      lease,
		GitName:    agent.GitName,
		GitEmail:   agent.GitEmail,
		Outcome:    executionOutcome(ctx, launchErr),
		Checkpoint: launchResult.WorkspaceQuiesced,
	})
	result.WorkspaceResult = workspaceResult
	if launchErr != nil {
		launchErr = fmt.Errorf("launch agent session: %w", launchErr)
	}
	if finalizeErr != nil {
		finalizeErr = fmt.Errorf("finalize private workspace: %w", finalizeErr)
	}
	return result, errors.Join(launchErr, finalizeErr)
}

func executionOutcome(ctx context.Context, launchErr error) ExecutionOutcome {
	if launchErr == nil {
		return ExecutionSucceeded
	}
	if ctx.Err() != nil {
		return ExecutionCanceled
	}
	return ExecutionFailed
}

func selectAgent(agents []Agent, requested string) (Agent, error) {
	seen := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		if strings.TrimSpace(agent.Name) == "" {
			return Agent{}, fmt.Errorf("configured agent has an empty name")
		}
		if _, exists := seen[agent.Name]; exists {
			return Agent{}, fmt.Errorf("agent %q is configured more than once", agent.Name)
		}
		seen[agent.Name] = struct{}{}
		if strings.TrimSpace(agent.Tool) == "" {
			return Agent{}, fmt.Errorf("agent %q has an empty tool", agent.Name)
		}
		if strings.TrimSpace(agent.GitName) == "" || strings.TrimSpace(agent.GitEmail) == "" {
			return Agent{}, fmt.Errorf("agent %q has an incomplete git identity", agent.Name)
		}
	}

	if requested != "" {
		for _, agent := range agents {
			if agent.Name == requested {
				return agent, nil
			}
		}
		return Agent{}, fmt.Errorf("agent %q is not configured", requested)
	}
	if len(agents) == 0 {
		return Agent{}, fmt.Errorf("no agents are configured")
	}
	if len(agents) != 1 {
		return Agent{}, fmt.Errorf("multiple agents are configured; pass --agent NAME to select one")
	}
	return agents[0], nil
}

func agentArgv(agent Agent, forwarded []string) []string {
	argv := []string{"run", "--agent", agent.Name, "--repo", containerWorkspacePath, "--", agent.Tool}
	return append(argv, forwarded...)
}
