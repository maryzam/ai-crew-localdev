package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/app/workspacelifecycle"
)

func TestWorkspaceListMakesRetainedResultsDiscoverable(t *testing.T) {
	store := &fakeWorkspaceStore{workspaces: []workspacelifecycle.CatalogEntry{{ID: "0123456789abcdef01234567", State: "result-ready", Repository: "owner/repo", SourceRoot: "/src/repo\u202e"}}}
	command := newWorkspaceCommandWithFactory(func() workspaceStore { return store })
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"list"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"0123456789abcdef01234567", "result-ready", "owner/repo", `\u{202e}`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output %q missing %q", output.String(), expected)
		}
	}
}

func TestWorkspaceListShowsUnreadableEntriesWithoutHidingHealthyEntries(t *testing.T) {
	store := &fakeWorkspaceStore{workspaces: []workspacelifecycle.CatalogEntry{
		{ID: "0123456789abcdef01234567", State: "unreadable", Details: "metadata version is unsupported"},
		{ID: "89abcdef0123456701234567", State: "active", Repository: "owner/repo", SourceRoot: "/src/repo"},
	}}
	command := newWorkspaceCommandWithFactory(func() workspaceStore { return store })
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"list"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"unreadable", "metadata version is unsupported", "active", "owner/repo"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output %q missing %q", output.String(), expected)
		}
	}
}

func TestWorkspaceRemoveRequiresExplicitForceForwarding(t *testing.T) {
	store := &fakeWorkspaceStore{removed: workspacelifecycle.Workspace{ID: "0123456789abcdef01234567"}}
	command := newWorkspaceCommandWithFactory(func() workspaceStore { return store })
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"remove", "0123456789abcdef01234567", "--force"})
	err := command.Execute()
	if err != nil || !store.force || store.workspaceID != "0123456789abcdef01234567" || !strings.Contains(output.String(), "Removed workspace") {
		t.Fatalf("error = %v, force = %t, id = %s, output = %q", err, store.force, store.workspaceID, output.String())
	}
}

type fakeWorkspaceStore struct {
	workspaces  []workspacelifecycle.CatalogEntry
	removed     workspacelifecycle.Workspace
	err         error
	workspaceID string
	force       bool
}

func (store *fakeWorkspaceStore) List(context.Context) ([]workspacelifecycle.CatalogEntry, error) {
	return append([]workspacelifecycle.CatalogEntry(nil), store.workspaces...), store.err
}

func (store *fakeWorkspaceStore) Remove(_ context.Context, workspaceID string, force bool) (workspacelifecycle.Workspace, error) {
	store.workspaceID = workspaceID
	store.force = force
	if store.err != nil {
		return workspacelifecycle.Workspace{}, store.err
	}
	if store.removed.ID == "" {
		return workspacelifecycle.Workspace{}, errors.New("not found")
	}
	return store.removed, nil
}
