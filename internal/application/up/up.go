package up

import (
	"context"
	"fmt"
)

type Input struct {
	Workspace string
	Project   string
	Runtime   string
	Build     bool
	Langfuse  bool
}

type LaunchInput struct {
	DevcontainerBin string
	Workspace       string
	Target          string
	Runtime         string
	Build           bool
}

type Result struct {
	Workspace string
	Target    string
	Runtime   string
	Project   bool
}

type Workspace interface {
	Prepare(context.Context, Input) (string, error)
}

type Readiness interface {
	EnsureHost(context.Context, string) (string, error)
	EnsureManaged(context.Context, string) (string, error)
}

type Setup interface {
	EnsureConfigured(context.Context) error
}

type Observability interface {
	Start(context.Context) error
}

type Broker interface {
	EnsureRunning(context.Context) error
}

type Container interface {
	FindCLI(context.Context) (string, error)
	FindGenericRoot(context.Context) (string, error)
	LaunchGeneric(context.Context, LaunchInput) error
	LaunchProject(context.Context, LaunchInput) error
}

type Ports struct {
	Workspace     Workspace
	Readiness     Readiness
	Setup         Setup
	Observability Observability
	Broker        Broker
	Container     Container
}

type UseCase struct {
	ports Ports
}

func New(ports Ports) (UseCase, error) {
	if ports.Workspace == nil || ports.Readiness == nil || ports.Setup == nil || ports.Observability == nil || ports.Broker == nil || ports.Container == nil {
		return UseCase{}, fmt.Errorf("up application ports must be configured")
	}
	return UseCase{ports: ports}, nil
}

func (u UseCase) Run(ctx context.Context, input Input) (Result, error) {
	workspace, err := u.ports.Workspace.Prepare(ctx, input)
	if err != nil {
		return Result{}, fmt.Errorf("resolve workspace: %w", err)
	}

	runtime, err := u.ports.Readiness.EnsureHost(ctx, input.Runtime)
	if err != nil {
		return Result{}, err
	}
	if err := u.ports.Setup.EnsureConfigured(ctx); err != nil {
		return Result{}, err
	}
	if input.Langfuse {
		if err := u.ports.Observability.Start(ctx); err != nil {
			return Result{}, fmt.Errorf("langfuse startup: %w", err)
		}
	}
	if err := u.ports.Broker.EnsureRunning(ctx); err != nil {
		return Result{}, fmt.Errorf("broker startup: %w", err)
	}
	runtime, err = u.ports.Readiness.EnsureManaged(ctx, runtime)
	if err != nil {
		return Result{}, err
	}
	devcontainerBin, err := u.ports.Container.FindCLI(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("devcontainer CLI not found in PATH: %w", err)
	}

	result := Result{Workspace: workspace, Runtime: runtime}
	if input.Project != "" {
		result.Target = workspace
		result.Project = true
		err = u.ports.Container.LaunchProject(ctx, LaunchInput{DevcontainerBin: devcontainerBin, Workspace: workspace, Target: workspace, Runtime: runtime, Build: input.Build})
		return result, err
	}

	result.Target, err = u.ports.Container.FindGenericRoot(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("find devcontainer root: %w", err)
	}
	err = u.ports.Container.LaunchGeneric(ctx, LaunchInput{DevcontainerBin: devcontainerBin, Workspace: workspace, Target: result.Target, Runtime: runtime, Build: input.Build})
	return result, err
}
