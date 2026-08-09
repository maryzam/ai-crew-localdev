package applysession

import (
	"context"
	"fmt"
	"strings"
)

type Workspace struct {
	ID           string
	BaseCommit   string
	ResultCommit string
}

type WorkspaceManager interface {
	Active(context.Context, string) (Workspace, error)
	Load(context.Context, string, string) (Workspace, error)
	Apply(context.Context, Workspace) (Workspace, error)
}

type Request struct {
	SourcePath  string
	WorkspaceID string
}

type Result struct {
	Workspace Workspace
	Changed   bool
}

type UseCase struct {
	workspaces WorkspaceManager
}

func New(workspaces WorkspaceManager) *UseCase {
	return &UseCase{workspaces: workspaces}
}

func (useCase *UseCase) Apply(ctx context.Context, request Request) (Result, error) {
	if useCase == nil || useCase.workspaces == nil {
		return Result{}, fmt.Errorf("apply session dependencies are not configured")
	}
	if strings.TrimSpace(request.SourcePath) == "" {
		return Result{}, fmt.Errorf("source repository path is required")
	}
	selected, err := useCase.selectWorkspace(ctx, request)
	if err != nil {
		return Result{}, err
	}
	applied, err := useCase.workspaces.Apply(ctx, selected)
	result := Result{Workspace: applied, Changed: applied.ResultCommit != "" && applied.ResultCommit != applied.BaseCommit}
	if err != nil {
		return result, fmt.Errorf("apply private workspace: %w", err)
	}
	return result, nil
}

func (useCase *UseCase) selectWorkspace(ctx context.Context, request Request) (Workspace, error) {
	if strings.TrimSpace(request.WorkspaceID) == "" {
		active, err := useCase.workspaces.Active(ctx, request.SourcePath)
		if err != nil {
			return Workspace{}, fmt.Errorf("load active private workspace: %w", err)
		}
		return active, nil
	}
	selected, err := useCase.workspaces.Load(ctx, request.SourcePath, request.WorkspaceID)
	if err != nil {
		return Workspace{}, fmt.Errorf("load selected private workspace: %w", err)
	}
	return selected, nil
}
