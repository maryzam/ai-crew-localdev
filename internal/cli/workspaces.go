package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/maryzam/ai-crew-localdev/internal/app/workspacelifecycle"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/devcontainer"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/uphost"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/workspace"
	"github.com/spf13/cobra"
)

type workspaceStore interface {
	List(context.Context) ([]workspacelifecycle.CatalogEntry, error)
	Remove(context.Context, string, bool) (workspacelifecycle.Workspace, error)
}

type workspaceStoreFactory func() workspaceStore

func newWorkspaceCommand() *cobra.Command {
	return newWorkspaceCommandWithFactory(func() workspaceStore {
		manager := workspace.NewManager(paths.DataDir())
		return workspacelifecycle.New(workspaceLifecyclePort{manager: manager}, workspaceContainerReconciler{})
	})
}

type workspaceLifecyclePort struct {
	manager workspace.Manager
}

func (port workspaceLifecyclePort) List(ctx context.Context) ([]workspacelifecycle.CatalogEntry, error) {
	listed, err := port.manager.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]workspacelifecycle.CatalogEntry, 0, len(listed))
	for _, retained := range listed {
		result = append(result, workspaceCatalogResult(retained))
	}
	return result, nil
}

func (port workspaceLifecyclePort) Remove(ctx context.Context, workspaceID string, force bool) (workspacelifecycle.Workspace, error) {
	removed, err := port.manager.Remove(ctx, workspaceID, force)
	return workspaceLifecycleResult(removed), err
}

func (port workspaceLifecyclePort) RemoveAfterContainerReconciliation(ctx context.Context, workspaceID string, force bool) (workspacelifecycle.Workspace, error) {
	removed, err := port.manager.RemoveAfterContainerReconciliation(ctx, workspaceID, force)
	return workspaceLifecycleResult(removed), err
}

func (port workspaceLifecyclePort) Resolve(ctx context.Context, workspaceID string) (workspacelifecycle.Workspace, error) {
	resolved, err := port.manager.LoadByID(ctx, workspaceID)
	return workspaceLifecycleResult(resolved), err
}

func (port workspaceLifecyclePort) Reconcile(ctx context.Context, selected workspacelifecycle.Workspace, action func(context.Context, workspacelifecycle.ContainerHandle) error) (workspacelifecycle.Workspace, error) {
	resolved, err := port.manager.LoadByID(ctx, selected.ID)
	if err != nil {
		return workspacelifecycle.Workspace{}, err
	}
	reconciled, err := port.manager.ReconcileContainer(ctx, resolved, func(reconcileCtx context.Context, handle workspace.ContainerHandle) error {
		return action(reconcileCtx, workspacelifecycle.ContainerHandle{Runtime: handle.Runtime, ID: handle.ID})
	})
	return workspaceLifecycleResult(reconciled), err
}

type workspaceContainerReconciler struct{}

func (workspaceContainerReconciler) Reconcile(ctx context.Context, selected workspacelifecycle.Workspace, handle workspacelifecycle.ContainerHandle) error {
	launcher := uphost.NewContainerLauncher(uphost.Streams{Out: io.Discard, Err: io.Discard}, nil)
	target := devcontainer.GenericRootPath(paths.DataDir(), selected.CheckoutPath)
	runtimes := []string{handle.Runtime}
	if handle.Runtime == "" {
		runtimes = []string{string(containerRuntimePodman), string(containerRuntimeDocker)}
	}
	for _, runtimeName := range runtimes {
		if err := launcher.ReconcileGenericContainer(ctx, target, runtimeName, handle.ID); err != nil {
			return err
		}
	}
	return devcontainer.RemoveGenericRoot(paths.DataDir(), selected.CheckoutPath)
}

func workspaceLifecycleResult(retained workspace.Workspace) workspacelifecycle.Workspace {
	return workspacelifecycle.Workspace{
		ID:                           retained.ID,
		State:                        string(retained.State),
		Repository:                   retained.Slug,
		SourceRoot:                   retained.SourceRoot,
		CheckoutPath:                 retained.CheckoutPath,
		NeedsContainerReconciliation: workspace.NeedsContainerReconciliation(retained),
		Container:                    workspacelifecycle.ContainerHandle{Runtime: retained.ContainerRuntime, ID: retained.ContainerID},
	}
}

func workspaceCatalogResult(retained workspace.CatalogEntry) workspacelifecycle.CatalogEntry {
	return workspacelifecycle.CatalogEntry{ID: retained.ID, State: retained.State, Repository: retained.Repository, SourceRoot: retained.SourceRoot, Details: retained.Problem, CreatedAt: retained.CreatedAt}
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
			_, _ = fmt.Fprintln(writer, "ID\tSTATE\tCREATED\tREPOSITORY\tSOURCE\tDETAILS")
			for _, retained := range workspaces {
				created := "unknown"
				if !retained.CreatedAt.IsZero() {
					created = retained.CreatedAt.UTC().Format("2006-01-02T15:04Z")
				}
				_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", retained.ID, retained.State, created, retained.Repository, terminalText(retained.SourceRoot), terminalText(retained.Details))
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
	command.Flags().BoolVar(&force, "force", false, "discard all recoverable workspace changes")
	return command
}
