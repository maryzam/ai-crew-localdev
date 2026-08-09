package workspacelifecycle

import (
	"context"
	"errors"
	"testing"
)

func TestUseCaseDelegatesLifecycleOperations(t *testing.T) {
	store := &fakeStore{workspaces: []CatalogEntry{{ID: "workspace-1"}}, resolved: Workspace{ID: "workspace-1"}, removed: Workspace{ID: "workspace-1"}}
	useCase := New(store, nil)
	listed, err := useCase.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].ID != "workspace-1" {
		t.Fatalf("listed = %+v, error = %v", listed, err)
	}
	removed, err := useCase.Remove(context.Background(), "workspace-1", true)
	if err != nil || removed.ID != "workspace-1" || store.workspaceID != "workspace-1" || !store.force {
		t.Fatalf("removed = %+v, error = %v, store = %+v", removed, err, store)
	}
}

func TestUseCaseValidatesDependenciesAndWorkspaceID(t *testing.T) {
	if _, err := New(nil, nil).List(context.Background()); err == nil {
		t.Fatal("missing store was accepted")
	}
	store := &fakeStore{}
	if _, err := New(store, nil).Remove(context.Background(), " ", false); err == nil {
		t.Fatal("empty workspace ID was accepted")
	}
	store.listErr = errors.New("storage failed")
	if _, err := New(store, nil).List(context.Background()); !errors.Is(err, store.listErr) {
		t.Fatalf("storage error = %v", err)
	}
}

func TestUseCaseReconcilesContainerBeforeRemoval(t *testing.T) {
	handle := ContainerHandle{Runtime: "podman", ID: "0123456789ab"}
	store := &fakeStore{
		resolved: Workspace{ID: "workspace-1", CheckoutPath: "/managed/repo", NeedsContainerReconciliation: true, Container: handle},
		removed:  Workspace{ID: "workspace-1"},
	}
	reconciler := &fakeContainerReconciler{}
	removed, err := New(store, reconciler).Remove(context.Background(), "workspace-1", false)
	if err != nil || removed.ID != "workspace-1" || reconciler.workspace.ID != "workspace-1" || reconciler.handle != handle {
		t.Fatalf("removed = %+v, reconciler = %+v, error = %v", removed, reconciler, err)
	}
}

func TestForceRemovalReconcilesUnreadableWorkspaceByDerivedCheckout(t *testing.T) {
	store := &fakeStore{
		resolved:   Workspace{ID: "workspace-1", CheckoutPath: "/managed/workspace-1/repo"},
		resolveErr: errors.New("metadata is unreadable"),
		removed:    Workspace{ID: "workspace-1"},
	}
	reconciler := &fakeContainerReconciler{}
	removed, err := New(store, reconciler).Remove(context.Background(), "workspace-1", true)
	if err != nil || removed.ID != "workspace-1" || reconciler.workspace.CheckoutPath != store.resolved.CheckoutPath || reconciler.handle != (ContainerHandle{}) {
		t.Fatalf("removed = %+v, reconciler = %+v, error = %v", removed, reconciler, err)
	}
}

type fakeStore struct {
	workspaces  []CatalogEntry
	removed     Workspace
	resolved    Workspace
	listErr     error
	resolveErr  error
	removeErr   error
	workspaceID string
	force       bool
}

type fakeContainerReconciler struct {
	workspace Workspace
	handle    ContainerHandle
}

func (reconciler *fakeContainerReconciler) Reconcile(_ context.Context, selected Workspace, handle ContainerHandle) error {
	reconciler.workspace = selected
	reconciler.handle = handle
	return nil
}

func (store *fakeStore) List(context.Context) ([]CatalogEntry, error) {
	return append([]CatalogEntry(nil), store.workspaces...), store.listErr
}

func (store *fakeStore) Resolve(context.Context, string) (Workspace, error) {
	return store.resolved, store.resolveErr
}

func (store *fakeStore) Reconcile(ctx context.Context, selected Workspace, action func(context.Context, ContainerHandle) error) (Workspace, error) {
	if err := action(ctx, selected.Container); err != nil {
		return Workspace{}, err
	}
	selected.NeedsContainerReconciliation = false
	selected.Container = ContainerHandle{}
	return selected, nil
}

func (store *fakeStore) Remove(_ context.Context, workspaceID string, force bool) (Workspace, error) {
	store.workspaceID = workspaceID
	store.force = force
	return store.removed, store.removeErr
}

func (store *fakeStore) RemoveAfterContainerReconciliation(_ context.Context, workspaceID string, force bool) (Workspace, error) {
	store.workspaceID = workspaceID
	store.force = force
	return store.removed, store.removeErr
}
