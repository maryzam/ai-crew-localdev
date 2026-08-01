package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

func (manager Manager) List(ctx context.Context) ([]Workspace, error) {
	operationCtx, cancel := context.WithTimeout(ctx, sourceInspectionTimeout)
	defer cancel()
	return manager.listWorkspaces(operationCtx)
}

func (manager Manager) Remove(ctx context.Context, workspaceID string, force bool) (Workspace, error) {
	if !workspaceIDPattern.MatchString(workspaceID) {
		return Workspace{}, fmt.Errorf("invalid workspace ID")
	}
	workspace, err := manager.findWorkspace(ctx, workspaceID)
	if err != nil {
		return Workspace{}, err
	}
	if workspace.State == StateRunning || workspace.State == StateApplying {
		return Workspace{}, fmt.Errorf("workspace %s is %s and cannot be removed", workspace.ID, workspace.State)
	}
	if !force && workspace.ResultCommit != "" && workspace.ResultCommit != workspace.BaseCommit && workspace.State != StateApplied {
		return Workspace{}, fmt.Errorf("workspace %s has an unapplied result; apply it or pass --force to discard it", workspace.ID)
	}
	err = withSourceLock(ctx, manager.sourceDirectory(workspace.sourceKey), func() error {
		current, loadErr := manager.loadWorkspace(workspace.sourceKey, workspace.ID)
		if loadErr != nil {
			return loadErr
		}
		if current.State == StateRunning || current.State == StateApplying {
			return fmt.Errorf("workspace %s is %s and cannot be removed", current.ID, current.State)
		}
		if !force && current.ResultCommit != "" && current.ResultCommit != current.BaseCommit && current.State != StateApplied {
			return fmt.Errorf("workspace %s has an unapplied result; apply it or pass --force to discard it", current.ID)
		}
		if err := manager.clearActive(current); err != nil {
			return err
		}
		if err := os.RemoveAll(manager.workspaceDirectory(current.sourceKey, current.ID)); err != nil {
			return fmt.Errorf("remove workspace %s: %w", current.ID, err)
		}
		return securefile.SyncDirectory(manager.sourceDirectory(current.sourceKey))
	})
	return workspace, err
}

func (manager Manager) findWorkspace(ctx context.Context, workspaceID string) (Workspace, error) {
	workspaces, err := manager.listWorkspaces(ctx)
	if err != nil {
		return Workspace{}, err
	}
	var found Workspace
	for _, candidate := range workspaces {
		if candidate.ID != workspaceID {
			continue
		}
		if found.ID != "" {
			return Workspace{}, fmt.Errorf("workspace ID %s is not unique", workspaceID)
		}
		found = candidate
	}
	if found.ID == "" {
		return Workspace{}, fmt.Errorf("workspace %s was not found", workspaceID)
	}
	return found, nil
}

func (manager Manager) listWorkspaces(ctx context.Context) ([]Workspace, error) {
	const sourceLimit = 4096
	const workspaceLimit = 16384
	entries, err := readDirectoryBounded(manager.root, sourceLimit)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list workspace storage: %w", err)
	}
	workspaces := make([]Workspace, 0)
	for _, source := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("list workspaces: %w", err)
		}
		if !source.IsDir() || !sourceKeyPattern.MatchString(source.Name()) {
			continue
		}
		children, readErr := readDirectoryBounded(manager.sourceDirectory(source.Name()), workspaceLimit-len(workspaces))
		if readErr != nil {
			return nil, fmt.Errorf("list workspace source %s: %w", source.Name(), readErr)
		}
		for _, child := range children {
			if !child.IsDir() || !workspaceIDPattern.MatchString(child.Name()) {
				continue
			}
			if len(workspaces) >= workspaceLimit {
				return nil, fmt.Errorf("workspace count exceeds %d", workspaceLimit)
			}
			loaded, loadErr := manager.loadWorkspace(source.Name(), child.Name())
			if loadErr != nil {
				return nil, fmt.Errorf("load workspace %s: %w", child.Name(), loadErr)
			}
			workspaces = append(workspaces, loaded)
		}
	}
	sort.Slice(workspaces, func(left, right int) bool { return workspaces[left].CreatedAt.Before(workspaces[right].CreatedAt) })
	return workspaces, nil
}

func readDirectoryBounded(path string, limit int) ([]os.DirEntry, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("directory entry budget exhausted")
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	entries, err := directory.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > limit {
		return nil, fmt.Errorf("directory %s exceeds %d entries", path, limit)
	}
	return entries, nil
}
