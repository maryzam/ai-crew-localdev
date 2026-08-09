package applysession

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestApplyLoadsActiveWorkspaceAndAppliesIt(t *testing.T) {
	manager := &fakeWorkspaceManager{
		active:  Workspace{ID: "workspace-1", BaseCommit: "base", ResultCommit: "result"},
		applied: Workspace{ID: "workspace-1", BaseCommit: "base", ResultCommit: "result"},
	}
	result, err := New(manager).Apply(context.Background(), Request{SourcePath: "/src/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if manager.sourcePath != "/src/repo" || manager.applyInput != manager.active || !result.Changed || result.Workspace != manager.applied {
		t.Fatalf("manager = %+v, result = %+v", manager, result)
	}
}

func TestApplyLoadsSelectedWorkspaceInsteadOfActive(t *testing.T) {
	manager := &fakeWorkspaceManager{
		loaded:  Workspace{ID: "workspace-older", BaseCommit: "base", ResultCommit: "result"},
		applied: Workspace{ID: "workspace-older", BaseCommit: "base", ResultCommit: "result"},
	}
	result, err := New(manager).Apply(context.Background(), Request{SourcePath: "/src/repo", WorkspaceID: "workspace-older"})
	if err != nil {
		t.Fatal(err)
	}
	if manager.workspaceID != "workspace-older" || manager.activeCalls != 0 || manager.applyInput != manager.loaded || result.Workspace != manager.applied {
		t.Fatalf("manager = %+v, result = %+v", manager, result)
	}
}

func TestApplyStopsAfterActiveWorkspaceFailure(t *testing.T) {
	activeErr := errors.New("no active workspace")
	manager := &fakeWorkspaceManager{activeErr: activeErr}
	_, err := New(manager).Apply(context.Background(), Request{SourcePath: "/src/repo"})
	if !errors.Is(err, activeErr) || manager.applyCalls != 0 {
		t.Fatalf("error = %v, apply calls = %d", err, manager.applyCalls)
	}
}

func TestApplyPreservesWorkspaceResultOnFailure(t *testing.T) {
	applyErr := errors.New("source moved")
	manager := &fakeWorkspaceManager{
		active:   Workspace{ID: "workspace-1", BaseCommit: "base", ResultCommit: "result"},
		applied:  Workspace{ID: "workspace-1", BaseCommit: "base", ResultCommit: "result"},
		applyErr: applyErr,
	}
	result, err := New(manager).Apply(context.Background(), Request{SourcePath: "/src/repo"})
	if !errors.Is(err, applyErr) || result.Workspace.ID != "workspace-1" || !result.Changed {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

func TestApplyRejectsMissingDependenciesAndSource(t *testing.T) {
	if _, err := New(nil).Apply(context.Background(), Request{SourcePath: "."}); err == nil || !strings.Contains(err.Error(), "dependencies") {
		t.Fatalf("dependency error = %v", err)
	}
	if _, err := New(&fakeWorkspaceManager{}).Apply(context.Background(), Request{SourcePath: " "}); err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("source error = %v", err)
	}
}

type fakeWorkspaceManager struct {
	sourcePath  string
	workspaceID string
	active      Workspace
	activeErr   error
	activeCalls int
	loaded      Workspace
	loadErr     error
	applyInput  Workspace
	applied     Workspace
	applyErr    error
	applyCalls  int
}

func (manager *fakeWorkspaceManager) Active(_ context.Context, sourcePath string) (Workspace, error) {
	manager.sourcePath = sourcePath
	manager.activeCalls++
	return manager.active, manager.activeErr
}

func (manager *fakeWorkspaceManager) Load(_ context.Context, sourcePath, workspaceID string) (Workspace, error) {
	manager.sourcePath = sourcePath
	manager.workspaceID = workspaceID
	return manager.loaded, manager.loadErr
}

func (manager *fakeWorkspaceManager) Apply(_ context.Context, workspace Workspace) (Workspace, error) {
	manager.applyCalls++
	manager.applyInput = workspace
	return manager.applied, manager.applyErr
}
