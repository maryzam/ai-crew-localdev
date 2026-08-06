package workspacelifecycle

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Workspace struct {
	ID                           string
	State                        string
	Repository                   string
	SourceRoot                   string
	CheckoutPath                 string
	NeedsContainerReconciliation bool
	Container                    ContainerHandle
}

type ContainerHandle struct {
	Runtime string
	ID      string
}

type CatalogEntry struct {
	ID         string
	State      string
	Repository string
	SourceRoot string
	Details    string
	CreatedAt  time.Time
}

type Store interface {
	List(context.Context) ([]CatalogEntry, error)
	Resolve(context.Context, string) (Workspace, error)
	Reconcile(context.Context, Workspace, func(context.Context, ContainerHandle) error) (Workspace, error)
	Remove(context.Context, string, bool) (Workspace, error)
	RemoveAfterContainerReconciliation(context.Context, string, bool) (Workspace, error)
}

type ContainerReconciler interface {
	Reconcile(context.Context, Workspace, ContainerHandle) error
}

type UseCase struct {
	store      Store
	reconciler ContainerReconciler
}

func New(store Store, reconciler ContainerReconciler) *UseCase {
	return &UseCase{store: store, reconciler: reconciler}
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
	resolved, err := useCase.store.Resolve(ctx, workspaceID)
	if err != nil {
		if !force {
			return Workspace{}, err
		}
		if resolved.CheckoutPath == "" || useCase.reconciler == nil {
			return Workspace{}, fmt.Errorf("workspace %s metadata is unreadable and container reconciliation is unavailable: %w", workspaceID, err)
		}
		if reconcileErr := useCase.reconciler.Reconcile(ctx, resolved, ContainerHandle{}); reconcileErr != nil {
			return Workspace{}, fmt.Errorf("reconcile unreadable workspace %s container: %w", workspaceID, reconcileErr)
		}
		return useCase.store.RemoveAfterContainerReconciliation(ctx, workspaceID, true)
	}
	if resolved.NeedsContainerReconciliation {
		if useCase.reconciler == nil {
			return Workspace{}, fmt.Errorf("workspace %s container reconciliation is not configured", workspaceID)
		}
		if _, err := useCase.store.Reconcile(ctx, resolved, func(reconcileCtx context.Context, handle ContainerHandle) error {
			return useCase.reconciler.Reconcile(reconcileCtx, resolved, handle)
		}); err != nil {
			return Workspace{}, fmt.Errorf("reconcile workspace %s container: %w", workspaceID, err)
		}
	}
	return useCase.store.Remove(ctx, workspaceID, force)
}
