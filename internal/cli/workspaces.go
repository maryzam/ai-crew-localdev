package cli

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/maryzam/ai-crew-localdev/internal/app/workspacelifecycle"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/workspace"
	"github.com/spf13/cobra"
)

type workspaceStore interface {
	List(context.Context) ([]workspacelifecycle.Workspace, error)
	Remove(context.Context, string, bool) (workspacelifecycle.Workspace, error)
}

type workspaceStoreFactory func() workspaceStore

func newWorkspaceCommand() *cobra.Command {
	return newWorkspaceCommandWithFactory(func() workspaceStore {
		manager := workspace.NewManager(paths.DataDir())
		return workspacelifecycle.New(workspaceLifecyclePort{manager: manager})
	})
}

type workspaceLifecyclePort struct {
	manager workspace.Manager
}

func (port workspaceLifecyclePort) List(ctx context.Context) ([]workspacelifecycle.Workspace, error) {
	listed, err := port.manager.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]workspacelifecycle.Workspace, 0, len(listed))
	for _, retained := range listed {
		result = append(result, workspaceLifecycleResult(retained))
	}
	return result, nil
}

func (port workspaceLifecyclePort) Remove(ctx context.Context, workspaceID string, force bool) (workspacelifecycle.Workspace, error) {
	removed, err := port.manager.Remove(ctx, workspaceID, force)
	return workspaceLifecycleResult(removed), err
}

func workspaceLifecycleResult(retained workspace.Workspace) workspacelifecycle.Workspace {
	return workspacelifecycle.Workspace{ID: retained.ID, State: string(retained.State), Repository: retained.Slug, SourceRoot: retained.SourceRoot}
}

func newWorkspaceCommandWithFactory(factory workspaceStoreFactory) *cobra.Command {
	command := &cobra.Command{Use: "workspace", Short: "Inspect and remove retained private workspaces"}
	command.AddCommand(newWorkspaceListCommand(factory), newWorkspaceRemoveCommand(factory))
	return command
}

func newWorkspaceListCommand(factory workspaceStoreFactory) *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List retained private workspaces",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(command *cobra.Command, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("workspace list accepts no arguments")
			}
			workspaces, err := factory().List(commandContext(command))
			if err != nil {
				renderCLIError(command, "Workspace list", err)
				return err
			}
			if len(workspaces) == 0 {
				_, _ = fmt.Fprintln(command.OutOrStdout(), "No retained workspaces")
				return nil
			}
			writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(writer, "ID\tSTATE\tREPOSITORY\tSOURCE")
			for _, retained := range workspaces {
				_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", retained.ID, retained.State, retained.Repository, terminalText(retained.SourceRoot))
			}
			return writer.Flush()
		},
	}
}

func newWorkspaceRemoveCommand(factory workspaceStoreFactory) *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:           "remove <workspace-id>",
		Short:         "Remove a retained private workspace",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(command *cobra.Command, args []string) error {
			removed, err := factory().Remove(commandContext(command), args[0], force)
			if err != nil {
				renderCLIError(command, "Workspace remove", err)
				return err
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Removed workspace %s\n", removed.ID)
			return nil
		},
	}
	command.Flags().BoolVar(&force, "force", false, "discard an unapplied result")
	return command
}
