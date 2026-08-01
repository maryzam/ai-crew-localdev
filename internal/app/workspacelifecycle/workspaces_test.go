package workspacelifecycle

import (
	"context"
	"errors"
	"testing"
)

func TestUseCaseDelegatesLifecycleOperations(t *testing.T) {
	store := &fakeStore{workspaces: []Workspace{{ID: "workspace-1"}}, removed: Workspace{ID: "workspace-1"}}
	useCase := New(store)
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
	if _, err := New(nil).List(context.Background()); err == nil {
		t.Fatal("missing store was accepted")
	}
	store := &fakeStore{}
	if _, err := New(store).Remove(context.Background(), " ", false); err == nil {
		t.Fatal("empty workspace ID was accepted")
	}
	store.err = errors.New("storage failed")
	if _, err := New(store).List(context.Background()); !errors.Is(err, store.err) {
		t.Fatalf("storage error = %v", err)
	}
}

type fakeStore struct {
	workspaces  []Workspace
	removed     Workspace
	err         error
	workspaceID string
	force       bool
}

func (store *fakeStore) List(context.Context) ([]Workspace, error) {
	return append([]Workspace(nil), store.workspaces...), store.err
}

func (store *fakeStore) Remove(_ context.Context, workspaceID string, force bool) (Workspace, error) {
	store.workspaceID = workspaceID
	store.force = force
	return store.removed, store.err
}
