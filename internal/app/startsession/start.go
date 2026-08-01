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
	ID           string
	SourceRoot   string
	CheckoutPath string
	BaseCommit   string
}

type WorkspaceResult struct {
	ResultCommit string
	Changed      bool
}

type WorkspaceManager interface {
	Preflight(context.Context, WorkspaceRequest) error
	PrepareOrResume(context.Context, WorkspaceRequest) (Workspace, error)
	Acquire(context.Context, Workspace) (Lease, error)
	Finalize(context.Context, FinalizeRequest) (WorkspaceResult, error)
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
	Workspace Workspace
	Lease     Lease
	GitName   string
	GitEmail  string
	Outcome   ExecutionOutcome
}

type LaunchRequest struct {
	WorkspaceID  string
	CheckoutPath string
	Argv         []string
}

type ContainerLauncher interface {
	Launch(context.Context, LaunchRequest) error
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
	preflight := WorkspaceRequest{SourcePath: request.SourcePath, New: request.New}
	if err := useCase.workspaces.Preflight(ctx, preflight); err != nil {
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

	workspace, err := useCase.workspaces.PrepareOrResume(ctx, WorkspaceRequest{SourcePath: request.SourcePath, New: request.New, AgentName: agent.Name, Tool: agent.Tool})
	if err != nil {
		return Result{Agent: agent}, fmt.Errorf("prepare private workspace: %w", err)
	}
	result := Result{Agent: agent, Workspace: workspace}
	lease, err := useCase.workspaces.Acquire(ctx, workspace)
	if err != nil {
		return result, fmt.Errorf("acquire private workspace lease: %w", err)
	}

	launchErr := useCase.launcher.Launch(ctx, LaunchRequest{
		WorkspaceID:  workspace.ID,
		CheckoutPath: workspace.CheckoutPath,
		Argv:         agentArgv(agent, request.AgentArgs),
	})
	workspaceResult, finalizeErr := useCase.workspaces.Finalize(context.WithoutCancel(ctx), FinalizeRequest{
		Workspace: workspace,
		Lease:     lease,
		GitName:   agent.GitName,
		GitEmail:  agent.GitEmail,
		Outcome:   executionOutcome(ctx, launchErr),
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
	argv := []string{"ai-agent", "run", "--agent", agent.Name, "--repo", containerWorkspacePath, "--", agent.Tool}
	return append(argv, forwarded...)
}
