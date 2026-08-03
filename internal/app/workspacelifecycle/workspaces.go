package workspacelifecycle

import (
	"context"
	"fmt"
	"strings"
)

type Workspace struct {
	ID         string
	State      string
	Repository string
	SourceRoot string
}

type CatalogEntry struct {
	ID         string
	State      string
	Repository string
	SourceRoot string
	Details    string
}

type Store interface {
	List(context.Context) ([]CatalogEntry, error)
	Remove(context.Context, string, bool) (Workspace, error)
}

type UseCase struct {
	store Store
}

func New(store Store) *UseCase {
	return &UseCase{store: store}
}

func (useCase *UseCase) List(ctx context.Context) ([]CatalogEntry, error) {
	if useCase == nil || useCase.store == nil {
		return nil, fmt.Errorf("workspace lifecycle dependencies are not configured")
	}
	return useCase.store.List(ctx)
}

func (useCase *UseCase) Remove(ctx context.Context, workspaceID string, force bool) (Workspace, error) {
	if useCase == nil || useCase.store == nil {
		return Workspace{}, fmt.Errorf("workspace lifecycle dependencies are not configured")
	}
	if strings.TrimSpace(workspaceID) == "" {
		return Workspace{}, fmt.Errorf("workspace ID is required")
	}
	return useCase.store.Remove(ctx, workspaceID, force)
}
