package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

const catalogAdministrativeEntryAllowance = 16

type CatalogEntry struct {
	ID         string
	State      string
	Repository string
	SourceRoot string
	Problem    string
}

type workspaceLocation struct {
	sourceKey string
	id        string
	directory string
}

func (manager Manager) List(ctx context.Context) ([]CatalogEntry, error) {
	operationCtx, cancel := context.WithTimeout(ctx, sourceInspectionTimeout)
	defer cancel()
	return manager.listWorkspaces(operationCtx)
}

func (manager Manager) Remove(ctx context.Context, workspaceID string, force bool) (Workspace, error) {
	if !workspaceIDPattern.MatchString(workspaceID) {
		return Workspace{}, fmt.Errorf("invalid workspace ID")
	}
	location, err := manager.findWorkspaceLocation(ctx, workspaceID)
	if err != nil {
		return Workspace{}, err
	}
	runLock, err := acquireRunLock(ctx, filepath.Join(location.directory, "run.lock"))
	if err != nil {
		return Workspace{}, err
	}

	removed := Workspace{ID: workspaceID, sourceKey: location.sourceKey}
	operationErr := withSourceLock(ctx, manager.sourceDirectory(location.sourceKey), func() error {
		current, loadErr := manager.loadWorkspace(location.sourceKey, workspaceID)
		if loadErr != nil {
			if !force {
				return fmt.Errorf("workspace %s metadata is unreadable; pass --force to discard it: %w", workspaceID, loadErr)
			}
		} else {
			removed = current
			if !force {
				if inspectErr := manager.rejectRecoverableWorkspace(ctx, current); inspectErr != nil {
					return inspectErr
				}
			}
		}
		if err := manager.clearActiveID(location.sourceKey, workspaceID); err != nil {
			return err
		}
		if err := os.RemoveAll(location.directory); err != nil {
			return fmt.Errorf("remove workspace %s: %w", workspaceID, err)
		}
		return securefile.SyncDirectory(manager.sourceDirectory(location.sourceKey))
	})
	return removed, errors.Join(operationErr, releaseRunLock(runLock))
}

func (manager Manager) rejectRecoverableWorkspace(ctx context.Context, retained Workspace) error {
	if err := manager.secureCheckoutConfig(ctx, retained); err != nil {
		return fmt.Errorf("inspect workspace %s before removal: %w", retained.ID, err)
	}
	status, err := manager.git.run(ctx, retained.CheckoutPath, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("inspect workspace %s changes before removal: %w", retained.ID, err)
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("workspace %s has recoverable changes; apply them or pass --force to discard them", retained.ID)
	}
	head, err := manager.git.run(ctx, retained.CheckoutPath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fmt.Errorf("inspect workspace %s history before removal: %w", retained.ID, err)
	}
	if head != retained.BaseCommit {
		return fmt.Errorf("workspace %s has an unapplied result; apply it or pass --force to discard it", retained.ID)
	}
	return nil
}

func (manager Manager) findWorkspaceLocation(ctx context.Context, workspaceID string) (workspaceLocation, error) {
	entries, err := readDirectoryBounded(manager.root, 4096)
	if errors.Is(err, os.ErrNotExist) {
		return workspaceLocation{}, fmt.Errorf("workspace %s was not found", workspaceID)
	}
	if err != nil {
		return workspaceLocation{}, fmt.Errorf("list workspace storage: %w", err)
	}
	var found workspaceLocation
	for _, source := range entries {
		if err := ctx.Err(); err != nil {
			return workspaceLocation{}, fmt.Errorf("find workspace: %w", err)
		}
		if !source.IsDir() || !sourceKeyPattern.MatchString(source.Name()) {
			continue
		}
		directory := manager.workspaceDirectory(source.Name(), workspaceID)
		info, statErr := os.Lstat(directory)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return workspaceLocation{}, fmt.Errorf("inspect workspace %s: %w", workspaceID, statErr)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 || stat.Uid != uint32(os.Getuid()) {
			return workspaceLocation{}, fmt.Errorf("workspace %s directory must be owner-only", workspaceID)
		}
		if found.id != "" {
			return workspaceLocation{}, fmt.Errorf("workspace ID %s is not unique", workspaceID)
		}
		found = workspaceLocation{sourceKey: source.Name(), id: workspaceID, directory: directory}
	}
	if found.id == "" {
		return workspaceLocation{}, fmt.Errorf("workspace %s was not found", workspaceID)
	}
	return found, nil
}

func (manager Manager) listWorkspaces(ctx context.Context) ([]CatalogEntry, error) {
	const sourceLimit = 4096
	const workspaceLimit = 16384
	entries, err := readDirectoryBounded(manager.root, sourceLimit)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list workspace storage: %w", err)
	}
	workspaces := make([]CatalogEntry, 0)
	for _, source := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("list workspaces: %w", err)
		}
		if !source.IsDir() || !sourceKeyPattern.MatchString(source.Name()) {
			continue
		}
		remaining := workspaceLimit - len(workspaces)
		children, readErr := readDirectoryBounded(manager.sourceDirectory(source.Name()), remaining+catalogAdministrativeEntryAllowance)
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
				workspaces = append(workspaces, CatalogEntry{ID: child.Name(), State: "unreadable", Problem: loadErr.Error()})
				continue
			}
			workspaces = append(workspaces, CatalogEntry{ID: loaded.ID, State: string(loaded.State), Repository: loaded.Slug, SourceRoot: loaded.SourceRoot})
		}
	}
	sort.Slice(workspaces, func(left, right int) bool { return workspaces[left].ID < workspaces[right].ID })
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
