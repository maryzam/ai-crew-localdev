package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/app/applysession"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/workspace"
	"github.com/spf13/cobra"
)

type applyUseCase interface {
	Apply(context.Context, applysession.Request) (applysession.Result, error)
}

type applyUseCaseFactory func(*cobra.Command) applyUseCase

func newApplyCommand() *cobra.Command {
	return newApplyCommandWithFactory(defaultApplyUseCase)
}

func newApplyCommandWithFactory(factory applyUseCaseFactory) *cobra.Command {
	var workspaceID string
	command := &cobra.Command{
		Use:           "apply [repository]",
		Short:         "Fast-forward a private workspace result into its source repository",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	command.RunE = func(command *cobra.Command, args []string) error {
		if len(args) > 1 {
			err := fmt.Errorf("apply accepts at most one repository path")
			renderCLIError(command, "Apply", err)
			return err
		}
		sourcePath := "."
		if len(args) == 1 {
			sourcePath = args[0]
		}
		result, err := factory(command).Apply(commandContext(command), applysession.Request{SourcePath: sourcePath, WorkspaceID: workspaceID})
		if err != nil {
			if result.Workspace.ID != "" {
				_, _ = fmt.Fprintf(command.OutOrStdout(), "Workspace %s result remains private and unapplied\n", result.Workspace.ID)
			}
			renderCLIError(command, "Apply", err)
			return err
		}
		renderApplyResult(command, result)
		return nil
	}
	command.Flags().StringVar(&workspaceID, "workspace", "", "workspace ID to apply instead of the active workspace")
	command.SetFlagErrorFunc(func(command *cobra.Command, err error) error {
		renderCLIError(command, "Apply", err)
		return err
	})
	return command
}

func defaultApplyUseCase(command *cobra.Command) applyUseCase {
	manager := workspace.NewManager(paths.DataDir())
	manager.Observer = workspaceObserver(command)
	port := &applyWorkspacePort{manager: manager, loaded: make(map[string]workspace.Workspace)}
	return applysession.New(port)
}

func renderApplyResult(command *cobra.Command, result applysession.Result) {
	if result.Workspace.ID == "" {
		return
	}
	if result.Changed {
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Applied workspace %s at %s\n", result.Workspace.ID, result.Workspace.ResultCommit)
		return
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Workspace %s had no repository changes to apply\n", result.Workspace.ID)
}

type applyWorkspacePort struct {
	manager workspace.Manager
	loaded  map[string]workspace.Workspace
}

func (port *applyWorkspacePort) Active(ctx context.Context, sourcePath string) (applysession.Workspace, error) {
	active, err := port.manager.Active(ctx, sourcePath)
	if err != nil {
		return applysession.Workspace{}, err
	}
	port.loaded[active.ID] = active
	return applysession.Workspace{ID: active.ID, BaseCommit: active.BaseCommit, ResultCommit: active.ResultCommit}, nil
}

func (port *applyWorkspacePort) Load(ctx context.Context, sourcePath, workspaceID string) (applysession.Workspace, error) {
	selected, err := port.manager.Load(ctx, sourcePath, workspaceID)
	if err != nil {
		return applysession.Workspace{}, err
	}
	port.loaded[selected.ID] = selected
	return applysession.Workspace{ID: selected.ID, BaseCommit: selected.BaseCommit, ResultCommit: selected.ResultCommit}, nil
}

func (port *applyWorkspacePort) Apply(ctx context.Context, selected applysession.Workspace) (applysession.Workspace, error) {
	active, ok := port.loaded[selected.ID]
	if !ok {
		return applysession.Workspace{}, fmt.Errorf("workspace %s was not loaded for apply", selected.ID)
	}
	applied, err := port.manager.Apply(ctx, active)
	return applysession.Workspace{ID: applied.ID, BaseCommit: applied.BaseCommit, ResultCommit: applied.ResultCommit}, err
}

func workspaceObserver(command *cobra.Command) workspace.Observer {
	return func(event workspace.Event) {
		switch event.Outcome {
		case workspace.OutcomeStarted:
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Workspace %s started", event.Stage)
			if event.Budget > 0 {
				_, _ = fmt.Fprintf(command.OutOrStdout(), " (budget %s)", event.Budget)
			}
			_, _ = fmt.Fprintln(command.OutOrStdout())
		case workspace.OutcomeSucceeded:
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Workspace %s completed in %s\n", event.Stage, event.Elapsed.Round(time.Millisecond))
		case workspace.OutcomeFailed:
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Workspace %s failed after %s\n", event.Stage, event.Elapsed.Round(time.Millisecond))
		}
	}
}
