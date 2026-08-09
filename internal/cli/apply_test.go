package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/app/applysession"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/workspace"
	"github.com/spf13/cobra"
)

func TestApplyCommandDefaultsToCurrentRepositoryAndRendersResult(t *testing.T) {
	runner := &fakeApplyUseCase{result: applysession.Result{Workspace: applysession.Workspace{ID: "workspace-1", BaseCommit: "base", ResultCommit: "result"}, Changed: true}}
	command := newApplyCommandWithFactory(func(*cobra.Command) applyUseCase { return runner })
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(nil)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if runner.request != (applysession.Request{SourcePath: "."}) || !strings.Contains(output.String(), "Applied workspace workspace-1 at result") {
		t.Fatalf("request = %+v, output = %q", runner.request, output.String())
	}
}

func TestApplyCommandAcceptsExplicitRepository(t *testing.T) {
	runner := &fakeApplyUseCase{result: applysession.Result{Workspace: applysession.Workspace{ID: "workspace-1", BaseCommit: "base", ResultCommit: "base"}}}
	command := newApplyCommandWithFactory(func(*cobra.Command) applyUseCase { return runner })
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"/src/repo"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if runner.request != (applysession.Request{SourcePath: "/src/repo"}) || !strings.Contains(output.String(), "no repository changes") {
		t.Fatalf("request = %+v, output = %q", runner.request, output.String())
	}
}

func TestApplyCommandSelectsOlderWorkspace(t *testing.T) {
	runner := &fakeApplyUseCase{result: applysession.Result{Workspace: applysession.Workspace{ID: "workspace-older", BaseCommit: "base", ResultCommit: "result"}, Changed: true}}
	command := newApplyCommandWithFactory(func(*cobra.Command) applyUseCase { return runner })
	command.SetArgs([]string{"/src/repo", "--workspace", "workspace-older"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if runner.request != (applysession.Request{SourcePath: "/src/repo", WorkspaceID: "workspace-older"}) {
		t.Fatalf("request = %+v", runner.request)
	}
}

func TestApplyCommandPreservesUseCaseFailure(t *testing.T) {
	applyErr := errors.New("source moved")
	runner := &fakeApplyUseCase{result: applysession.Result{Workspace: applysession.Workspace{ID: "workspace-1", BaseCommit: "base", ResultCommit: "result"}, Changed: true}, err: applyErr}
	command := newApplyCommandWithFactory(func(*cobra.Command) applyUseCase { return runner })
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(nil)
	err := command.Execute()
	if !errors.Is(err, applyErr) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(output.String(), "Applied workspace") || !strings.Contains(output.String(), "remains private and unapplied") || !strings.Contains(output.String(), "Apply failed: source moved") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestApplyCommandRendersArgumentFailure(t *testing.T) {
	command := newApplyCommandWithFactory(func(*cobra.Command) applyUseCase { return &fakeApplyUseCase{} })
	var output bytes.Buffer
	command.SetErr(&output)
	command.SetArgs([]string{"one", "two"})
	if err := command.Execute(); err == nil || !strings.Contains(output.String(), "Apply failed: apply accepts at most one repository") {
		t.Fatalf("error output = %q", output.String())
	}
}

func TestWorkspaceObserverReportsSlowBoundedOperations(t *testing.T) {
	command := &cobra.Command{}
	var output bytes.Buffer
	command.SetOut(&output)
	observer := workspaceObserver(command)
	observer(workspace.Event{Stage: workspace.StageClone, Outcome: workspace.OutcomeStarted})
	observer(workspace.Event{Stage: workspace.StageAcquire, Outcome: workspace.OutcomeStarted})
	observer(workspace.Event{Stage: workspace.StagePrepare, Outcome: workspace.OutcomeSucceeded, WorkspaceID: "0123456789abcdef01234567"})
	observer(workspace.Event{Stage: workspace.StageApply, Outcome: workspace.OutcomeStarted})
	for _, expected := range []string{"Copying repository into private workspace", "Workspace 0123456789abcdef01234567 ready", "Applying private workspace result"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output %q missing %q", output.String(), expected)
		}
	}
}

type fakeApplyUseCase struct {
	request applysession.Request
	result  applysession.Result
	err     error
}

func (useCase *fakeApplyUseCase) Apply(_ context.Context, request applysession.Request) (applysession.Result, error) {
	useCase.request = request
	return useCase.result, useCase.err
}
