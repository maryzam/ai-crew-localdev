package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPrepareResolvesTopLevelAndCreatesIndependentCheckout(t *testing.T) {
	source := newSourceRepository(t)
	nested := filepath.Join(source, "nested", "path")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "misdirected"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())

	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: nested})
	if err != nil {
		t.Fatal(err)
	}
	if created.SourceRoot != source {
		t.Fatalf("source root = %q, want %q", created.SourceRoot, source)
	}
	if created.BaseCommit != gitOutput(t, source, "rev-parse", "HEAD") {
		t.Fatalf("base commit = %q", created.BaseCommit)
	}
	if created.Slug != "owner/repo" || created.Remote != "https://github.com/owner/repo.git" {
		t.Fatalf("repository identity = %q %q", created.Slug, created.Remote)
	}
	if !commitPattern.MatchString(created.PlanDigest) {
		t.Fatalf("plan digest = %q", created.PlanDigest)
	}
	if got := gitOutput(t, created.CheckoutPath, "remote", "get-url", "origin"); got != created.Remote {
		t.Fatalf("checkout origin = %q, want %q", got, created.Remote)
	}
	if created.CheckoutPath == source || !strings.HasPrefix(created.CheckoutPath, manager.Root()+string(os.PathSeparator)) {
		t.Fatalf("checkout path = %q, want an isolated managed path", created.CheckoutPath)
	}
	if got := gitOutput(t, created.CheckoutPath, "rev-parse", "--git-dir"); got != ".git" {
		t.Fatalf("git dir = %q, want self-contained .git", got)
	}
	if _, err := os.Stat(filepath.Join(created.CheckoutPath, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Fatalf("checkout has external object alternates: %v", err)
	}
	sourceObject := gitOutput(t, source, "rev-parse", "HEAD")
	sourceObjectPath := filepath.Join(source, ".git", "objects", sourceObject[:2], sourceObject[2:])
	checkoutObjectPath := filepath.Join(created.CheckoutPath, ".git", "objects", sourceObject[:2], sourceObject[2:])
	if sourceInfo, sourceErr := os.Stat(sourceObjectPath); sourceErr == nil {
		checkoutInfo, checkoutErr := os.Stat(checkoutObjectPath)
		if checkoutErr != nil {
			t.Fatal(checkoutErr)
		}
		if os.SameFile(sourceInfo, checkoutInfo) {
			t.Fatal("checkout object is hard-linked to source object")
		}
	}
	assertOwnerOnlyDirectory(t, filepath.Dir(created.CheckoutPath))
	assertOwnerOnlyDirectory(t, created.CheckoutPath)

	if err := os.WriteFile(filepath.Join(source, "human.txt"), []byte("human"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(created.CheckoutPath, "human.txt")); !os.IsNotExist(err) {
		t.Fatalf("source edit appeared in checkout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "agent.txt"), []byte("agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(source, "agent.txt")); !os.IsNotExist(err) {
		t.Fatalf("checkout edit appeared in source: %v", err)
	}
}

func TestPrepareRejectsDirtyAndUnbornSources(t *testing.T) {
	manager := NewManager(t.TempDir())
	dirty := newSourceRepository(t)
	if err := os.WriteFile(filepath.Join(dirty, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: dirty}); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty source error = %v", err)
	}
	unborn := t.TempDir()
	runGit(t, unborn, "init", "-q")
	if _, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: unborn}); err == nil || !strings.Contains(err.Error(), "committed HEAD") {
		t.Fatalf("unborn source error = %v", err)
	}
}

func TestPreflightRefusesInvalidNewSourceWithoutCreatingWorkspaceStorage(t *testing.T) {
	source := newSourceRepository(t)
	if err := os.WriteFile(filepath.Join(source, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(t.TempDir())
	if err := manager.Preflight(context.Background(), PrepareRequest{SourcePath: source, New: true}); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("preflight error = %v", err)
	}
	if _, err := os.Stat(manager.Root()); !os.IsNotExist(err) {
		t.Fatalf("preflight created workspace storage: %v", err)
	}
}

func TestPreflightAllowsDirtySourceWhenResumingIsolatedWorkspace(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	if _, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "human.txt"), []byte("human"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.Preflight(context.Background(), PrepareRequest{SourcePath: source}); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRequiresCanonicalizableCredentialFreeHTTPSOrigin(t *testing.T) {
	for _, test := range []struct {
		name   string
		remote string
		want   string
	}{
		{name: "missing", remote: "", want: "origin remote"},
		{name: "ssh", remote: "git@github.com:owner/repo.git", want: "HTTPS"},
		{name: "credentials", remote: "https://token@github.com/owner/repo.git", want: "credentials"},
		{name: "host", remote: "https://example.com/owner/repo.git", want: "github.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := newSourceRepository(t)
			if test.remote == "" {
				runGit(t, source, "remote", "remove", "origin")
			} else {
				runGit(t, source, "remote", "set-url", "origin", test.remote)
			}
			_, err := NewManager(t.TempDir()).Prepare(context.Background(), PrepareRequest{SourcePath: source})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("prepare error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPrepareCanonicalizesHTTPSOrigin(t *testing.T) {
	source := newSourceRepository(t)
	runGit(t, source, "remote", "set-url", "origin", "https://github.com/owner/repo")
	created, err := NewManager(t.TempDir()).Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if created.Remote != "https://github.com/owner/repo.git" || created.Slug != "owner/repo" {
		t.Fatalf("repository identity = %q %q", created.Slug, created.Remote)
	}
}

func TestPrepareRejectsDetachedAndInProgressSources(t *testing.T) {
	t.Run("detached", func(t *testing.T) {
		source := newSourceRepository(t)
		runGit(t, source, "checkout", "--detach", "-q")
		_, err := NewManager(t.TempDir()).Prepare(context.Background(), PrepareRequest{SourcePath: source})
		if err == nil || !strings.Contains(err.Error(), "named branch") {
			t.Fatalf("prepare error = %v", err)
		}
	})
	t.Run("merge", func(t *testing.T) {
		source := newSourceRepository(t)
		if err := os.WriteFile(filepath.Join(source, ".git", "MERGE_HEAD"), []byte(gitOutput(t, source, "rev-parse", "HEAD")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := NewManager(t.TempDir()).Prepare(context.Background(), PrepareRequest{SourcePath: source})
		if err == nil || !strings.Contains(err.Error(), "in-progress Git operation") {
			t.Fatalf("prepare error = %v", err)
		}
	})
}

func TestPrepareRejectsGitlinks(t *testing.T) {
	source := newSourceRepository(t)
	commit := gitOutput(t, source, "rev-parse", "HEAD")
	runGit(t, source, "update-index", "--add", "--cacheinfo", "160000,"+commit+",vendor/dependency")
	runGit(t, source, "commit", "-q", "-m", "gitlink")
	_, err := NewManager(t.TempDir()).Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err == nil || !strings.Contains(err.Error(), "gitlinks") {
		t.Fatalf("prepare error = %v", err)
	}
}

func TestPrepareResumesActiveWorkspaceUnlessNew(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	first, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != first.ID || resumed.CheckoutPath != first.CheckoutPath {
		t.Fatalf("resumed = %+v, want %+v", resumed, first)
	}
	second, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source, New: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("new workspace reused the active workspace ID")
	}
	loaded, err := manager.Load(context.Background(), source, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != first.ID || loaded.CheckoutPath != first.CheckoutPath {
		t.Fatalf("loaded = %+v, want %+v", loaded, first)
	}
}

func TestPrepareRefusesToResumeWorkspaceUnderAnotherAgent(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source, AgentName: "codex", Tool: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if created.AgentName != "codex" || created.Tool != "codex" {
		t.Fatalf("workspace identity = %q %q", created.AgentName, created.Tool)
	}
	_, err = manager.Prepare(context.Background(), PrepareRequest{SourcePath: source, AgentName: "claude", Tool: "claude"})
	if err == nil || !strings.Contains(err.Error(), "pass --new") {
		t.Fatalf("identity mismatch error = %v", err)
	}
	resumed, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source, AgentName: "codex", Tool: "codex"})
	if err != nil || resumed.ID != created.ID {
		t.Fatalf("same-agent resume = %+v, %v", resumed, err)
	}
}

func TestLoadRejectsWorkspaceFromAnotherSource(t *testing.T) {
	firstSource := newSourceRepository(t)
	secondSource := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: firstSource})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Load(context.Background(), secondSource, created.ID); err == nil || !strings.Contains(err.Error(), "load workspace") {
		t.Fatalf("cross-source load error = %v", err)
	}
}

func TestConcurrentPrepareReturnsOneActiveWorkspace(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	const count = 4
	results := make(chan Workspace, count)
	errors := make(chan error, count)
	var group sync.WaitGroup
	for range count {
		group.Add(1)
		go func() {
			defer group.Done()
			workspace, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
			results <- workspace
			errors <- err
		}()
	}
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	workspaceID := ""
	for workspace := range results {
		if workspaceID == "" {
			workspaceID = workspace.ID
		}
		if workspace.ID != workspaceID {
			t.Fatalf("concurrent prepare returned workspace %q, want %q", workspace.ID, workspaceID)
		}
	}
}

func TestPrepareResumeDoesNotRequireSourceToRemainClean(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	first, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "human.txt"), []byte("human"), 0o644); err != nil {
		t.Fatal(err)
	}
	resumed, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != first.ID {
		t.Fatalf("resumed workspace ID = %q, want %q", resumed.ID, first.ID)
	}
}

func TestCheckpointCommitsAllChangesAndPersistsMetadata(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "result.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkpointed, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if checkpointed.ResultCommit == "" || checkpointed.ResultCommit == checkpointed.BaseCommit || checkpointed.State != StateResultReady {
		t.Fatalf("checkpointed workspace = %+v", checkpointed)
	}
	resumed, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ResultCommit != checkpointed.ResultCommit || resumed.State != StateResultReady {
		t.Fatalf("resumed metadata = %+v, want result %q", resumed, checkpointed.ResultCommit)
	}
	if got := gitOutput(t, created.CheckoutPath, "show", "-s", "--format=%an <%ae>", "HEAD"); got != "Agent Bot <agent@example.test>" {
		t.Fatalf("checkpoint author = %q", got)
	}
}

func TestCheckpointAndApplyIgnoreAgentControlledGitConfiguration(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	gitDirectory := filepath.Join(created.CheckoutPath, ".git")
	marker := filepath.Join(gitDirectory, "agent-config-ran")
	fsmonitor := filepath.Join(gitDirectory, "evil-fsmonitor")
	if err := os.WriteFile(fsmonitor, []byte("#!/bin/sh\ntouch .git/agent-config-ran\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(gitDirectory, "evil-hooks")
	if err := os.Mkdir(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\ntouch .git/agent-config-ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	maliciousConfig := "[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tfsmonitor = .git/evil-fsmonitor\n\thooksPath = .git/evil-hooks\n[include]\n\tpath = /tmp/agent-controlled-git-config\n[filter \"evil\"]\n\tclean = .git/evil-fsmonitor\n"
	if err := os.WriteFile(filepath.Join(gitDirectory, "config"), []byte(maliciousConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, ".gitattributes"), []byte("result.txt filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "result.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	ready, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("agent-controlled Git configuration ran during checkpoint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDirectory, "config"), []byte(maliciousConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(context.Background(), ready); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("agent-controlled Git configuration ran during apply: %v", err)
	}
}

func TestApplyFastForwardsUnchangedCleanSource(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "result.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkpointed, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := manager.Apply(context.Background(), checkpointed)
	if err != nil {
		t.Fatal(err)
	}
	if applied.State != StateApplied || gitOutput(t, source, "rev-parse", "HEAD") != checkpointed.ResultCommit {
		t.Fatalf("applied workspace = %+v, source HEAD = %s", applied, gitOutput(t, source, "rev-parse", "HEAD"))
	}
	if content, err := os.ReadFile(filepath.Join(source, "result.txt")); err != nil || string(content) != "result" {
		t.Fatalf("applied result = %q, %v", content, err)
	}
}

func TestApplyDoesNotRunSourceHooks(t *testing.T) {
	source := newSourceRepository(t)
	marker := filepath.Join(source, ".git", "hook-ran")
	hook := filepath.Join(source, ".git", "hooks", "post-merge")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch .git/hook-ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "result.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkpointed, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(context.Background(), checkpointed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("source hook ran during apply: %v", err)
	}
}

func TestApplyRevalidatesSourceRepositoryIdentity(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "result.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	ready, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "remote", "set-url", "origin", "https://github.com/owner/other-repo.git")
	before := gitOutput(t, source, "rev-parse", "HEAD")
	if _, err := manager.Apply(context.Background(), ready); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("apply error = %v", err)
	}
	if after := gitOutput(t, source, "rev-parse", "HEAD"); after != before {
		t.Fatalf("source HEAD changed from %s to %s", before, after)
	}
	if _, err := os.Stat(filepath.Join(source, "result.txt")); !os.IsNotExist(err) {
		t.Fatalf("result was applied after identity refusal: %v", err)
	}
}

func TestCheckpointRejectsGitMetadataEscapesBeforeGitRuns(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
		want   string
	}{
		{name: "common-dir", mutate: func(t *testing.T, gitDirectory string) {
			if err := os.WriteFile(filepath.Join(gitDirectory, "commondir"), []byte("../../outside\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "external indirection"},
		{name: "alternates", mutate: func(t *testing.T, gitDirectory string) {
			path := filepath.Join(gitDirectory, "objects", "info", "alternates")
			if err := os.WriteFile(path, []byte("/tmp/outside-objects\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "external indirection"},
		{name: "objects-symlink", mutate: func(t *testing.T, gitDirectory string) {
			objects := filepath.Join(gitDirectory, "objects")
			if err := os.Rename(objects, objects+"-real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), objects); err != nil {
				t.Fatal(err)
			}
		}, want: "internal and owner-controlled"},
		{name: "refs-symlink", mutate: func(t *testing.T, gitDirectory string) {
			refs := filepath.Join(gitDirectory, "refs")
			if err := os.Rename(refs, refs+"-real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), refs); err != nil {
				t.Fatal(err)
			}
		}, want: "internal and owner-controlled"},
		{name: "logs-symlink", mutate: func(t *testing.T, gitDirectory string) {
			logs := filepath.Join(gitDirectory, "logs")
			if err := os.Rename(logs, logs+"-real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), logs); err != nil {
				t.Fatal(err)
			}
		}, want: "internal and owner-controlled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := newSourceRepository(t)
			manager := NewManager(t.TempDir())
			created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, filepath.Join(created.CheckoutPath, ".git"))
			if _, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("checkpoint error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestApplyRejectsGitMetadataEscapeIntroducedAfterCheckpoint(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	alternates := filepath.Join(created.CheckoutPath, ".git", "objects", "info", "alternates")
	if err := os.WriteFile(alternates, []byte("/tmp/outside-objects\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(context.Background(), ready); err == nil || !strings.Contains(err.Error(), "external indirection") {
		t.Fatalf("apply error = %v", err)
	}
}

func TestApplyRejectsExecutableSourceFiltersBeforeWorktreeUpdate(t *testing.T) {
	source := newSourceRepository(t)
	marker := filepath.Join(source, "filter-ran")
	runGit(t, source, "config", "filter.evil.smudge", "touch "+marker)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, ".gitattributes"), []byte("result.txt filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "result.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	ready, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(context.Background(), ready); err == nil || !strings.Contains(err.Error(), "executable Git filters") {
		t.Fatalf("apply error = %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("source filter executed during refused apply: %v", err)
	}
	if gitOutput(t, source, "rev-parse", "HEAD") != ready.BaseCommit {
		t.Fatal("source HEAD changed during filter refusal")
	}
}

func TestApplyVerifiesPostMergeSourceBeforePersistingApplied(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "result.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	ready, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\n" + shellVariable("real_git", realGit) + "\n\"$real_git\" \"$@\"\ncode=$?\nif [ \"$code\" -eq 0 ]; then\n  for arg in \"$@\"; do\n    if [ \"$arg\" = merge ]; then\n      \"$real_git\" -c core.hooksPath=/dev/null -C " + shellQuoteTest(source) + " reset --hard " + ready.BaseCommit + " >/dev/null\n    fi\n  done\nfi\nexit \"$code\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manager.git.path = wrapper
	if _, err := manager.Apply(context.Background(), ready); err == nil || !strings.Contains(err.Error(), "did not reach workspace result") {
		t.Fatalf("apply error = %v", err)
	}
	active, err := manager.Active(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if active.State != StateApplying {
		t.Fatalf("workspace state = %s, want applying evidence", active.State)
	}
}

func TestApplyIsIdempotentForVerifiedAppliedWorkspace(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := manager.Apply(context.Background(), ready)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := manager.Apply(context.Background(), applied)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.State != StateApplied || repeated.ResultCommit != applied.ResultCommit {
		t.Fatalf("repeated apply = %+v, want %+v", repeated, applied)
	}
}

func TestActiveLoadsWithoutCreatingAndIsClearedAfterApply(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	if _, err := manager.Active(context.Background(), source); err == nil || !strings.Contains(err.Error(), "no active workspace") {
		t.Fatalf("missing active error = %v", err)
	}
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	active, err := manager.Active(context.Background(), source)
	if err != nil || active.ID != created.ID {
		t.Fatalf("active = %+v, %v", active, err)
	}
	ready, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(context.Background(), ready); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Active(context.Background(), source); err == nil || !strings.Contains(err.Error(), "no active workspace") {
		t.Fatalf("applied active error = %v", err)
	}
}

func TestLifecyclePersistsOrderedTransitionEvidence(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source, AgentName: "codex", Tool: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(context.Background(), created)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "result.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	ready, err := manager.Complete(context.Background(), created, lease, Author{Name: "Agent Bot", Email: "agent@example.test"}, RunSucceeded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(context.Background(), ready); err != nil {
		t.Fatal(err)
	}
	applied, err := manager.Load(context.Background(), source, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantStates := []State{StateActive, StateRunning, StateResultReady, StateApplying, StateApplied}
	if len(applied.Transitions) != len(wantStates) {
		t.Fatalf("transitions = %+v", applied.Transitions)
	}
	for index, want := range wantStates {
		transition := applied.Transitions[index]
		if transition.Sequence != uint64(index+1) || transition.To != want || transition.Reason == "" || transition.BudgetMillis <= 0 || transition.ElapsedMillis < 0 {
			t.Fatalf("transition %d = %+v, want state %s", index, transition, want)
		}
	}
}

func TestOversizedTransitionEvidenceFailsWithoutReplacingMetadata(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	oversized := created
	for range 1024 {
		oversized.Transitions = append(oversized.Transitions, TransitionEvidence{Sequence: uint64(len(oversized.Transitions) + 1), At: time.Now().UTC(), From: StateActive, To: StateActive, Reason: strings.Repeat("e", 64), BudgetMillis: 1})
	}
	if err := manager.saveWorkspace(oversized); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized evidence error = %v", err)
	}
	loaded, err := manager.Load(context.Background(), source, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Transitions) != len(created.Transitions) {
		t.Fatalf("persisted transitions = %d, want %d", len(loaded.Transitions), len(created.Transitions))
	}
}

func TestPrepareEmitsBoundedOrderedProgress(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	var events []Event
	manager.Observer = func(event Event) { events = append(events, event) }
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source, New: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		stage   Stage
		outcome Outcome
	}{
		{StagePrepare, OutcomeStarted},
		{StageClone, OutcomeStarted},
		{StageClone, OutcomeSucceeded},
		{StagePrepare, OutcomeSucceeded},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v", events)
	}
	for index := range want {
		if events[index].Stage != want[index].stage || events[index].Outcome != want[index].outcome {
			t.Fatalf("events[%d] = %+v, want %+v", index, events[index], want[index])
		}
	}
	if events[2].WorkspaceID != created.ID || events[2].Budget != provisionTimeout || events[2].Elapsed < 0 {
		t.Fatalf("clone completion evidence = %+v", events[2])
	}
}

func TestApplyRefusesDirtyOrAdvancedSourceWithoutChangingHead(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
		want   string
	}{
		{name: "dirty", mutate: func(t *testing.T, source string) {
			if err := os.WriteFile(filepath.Join(source, "human.txt"), []byte("human"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "uncommitted changes"},
		{name: "advanced", mutate: func(t *testing.T, source string) {
			if err := os.WriteFile(filepath.Join(source, "human.txt"), []byte("human"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, source, "add", "human.txt")
			runGit(t, source, "commit", "-q", "-m", "human")
		}, want: "changed from base"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := newSourceRepository(t)
			manager := NewManager(t.TempDir())
			created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(created.CheckoutPath, "result.txt"), []byte("result"), 0o644); err != nil {
				t.Fatal(err)
			}
			checkpointed, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, source)
			before := gitOutput(t, source, "rev-parse", "HEAD")
			if _, err := manager.Apply(context.Background(), checkpointed); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("apply error = %v, want %q", err, test.want)
			}
			if after := gitOutput(t, source, "rev-parse", "HEAD"); after != before {
				t.Fatalf("source HEAD changed from %s to %s", before, after)
			}
			if _, err := os.Stat(filepath.Join(source, "result.txt")); !os.IsNotExist(err) {
				t.Fatalf("result was applied during refusal: %v", err)
			}
		})
	}
}

func TestApplyRefusesCheckoutChangesAfterCheckpoint(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	checkpointed, err := manager.checkpoint(context.Background(), created, Author{Name: "Agent Bot", Email: "agent@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "late.txt"), []byte("late"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := gitOutput(t, source, "rev-parse", "HEAD")
	if _, err := manager.Apply(context.Background(), checkpointed); err == nil || !strings.Contains(err.Error(), "changed after checkpoint") {
		t.Fatalf("apply error = %v", err)
	}
	if after := gitOutput(t, source, "rev-parse", "HEAD"); after != before {
		t.Fatalf("source HEAD changed from %s to %s", before, after)
	}
}

func TestRunLeaseExcludesConcurrentLaunchAndRecordsOutcome(t *testing.T) {
	source := newSourceRepository(t)
	dataDir := t.TempDir()
	firstManager := NewManager(dataDir)
	secondManager := NewManager(dataDir)
	created, err := firstManager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := firstManager.Acquire(context.Background(), created)
	if err != nil {
		t.Fatal(err)
	}
	running, err := firstManager.Load(context.Background(), source, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.State != StateRunning || running.LeaseID != lease.ID || running.LastReason != "launch_started" {
		t.Fatalf("running workspace = %+v", running)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	if _, err := secondManager.Acquire(waitCtx, created); err == nil || !strings.Contains(err.Error(), "lock running workspace") {
		t.Fatalf("concurrent acquire error = %v", err)
	}
	stillRunning, err := firstManager.Load(context.Background(), source, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillRunning.State != StateRunning || stillRunning.LeaseID != lease.ID {
		t.Fatalf("workspace changed after refused acquire: %+v", stillRunning)
	}

	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "result.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	completed, err := firstManager.Complete(context.Background(), created, lease, Author{Name: "Agent Bot", Email: "agent@example.test"}, RunSucceeded)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != StateResultReady || completed.LeaseID != "" || completed.LastOutcome != RunSucceeded || completed.LastReason != "launch_succeeded" {
		t.Fatalf("completed workspace = %+v", completed)
	}

	secondLease, err := secondManager.Acquire(context.Background(), completed)
	if err != nil {
		t.Fatal(err)
	}
	failedRun, err := secondManager.Complete(context.Background(), completed, secondLease, Author{Name: "Agent Bot", Email: "agent@example.test"}, RunFailed)
	if err != nil {
		t.Fatal(err)
	}
	if failedRun.State != StateResultReady || failedRun.LastOutcome != RunFailed || failedRun.LastReason != "launch_failed" {
		t.Fatalf("failed run outcome = %+v", failedRun)
	}
}

func TestAcquireReconcilesStaleRunningLeaseBeforeRelaunch(t *testing.T) {
	source := newSourceRepository(t)
	dataDir := t.TempDir()
	firstManager := NewManager(dataDir)
	created, err := firstManager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := firstManager.Acquire(context.Background(), created)
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseRunLock(stale.fd); err != nil {
		t.Fatal(err)
	}
	stale.fd = -1

	secondManager := NewManager(dataDir)
	reconciled, err := secondManager.Acquire(context.Background(), created)
	if err != nil {
		t.Fatal(err)
	}
	running, err := secondManager.Load(context.Background(), source, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.State != StateRunning || running.LeaseID != reconciled.ID || running.LastOutcome != RunInterrupted || running.LastReason != "stale_running_lease_reconciled" {
		t.Fatalf("reconciled workspace = %+v", running)
	}
	if _, err := secondManager.Complete(context.Background(), created, reconciled, Author{Name: "Agent Bot", Email: "agent@example.test"}, RunCanceled); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteFailurePersistsFailedStateAndReleasesLease(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(context.Background(), created)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.CheckoutPath, "result.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Complete(context.Background(), created, lease, Author{}, RunFailed); err == nil || !strings.Contains(err.Error(), "author name and email") {
		t.Fatalf("complete error = %v", err)
	}
	failed, err := manager.Load(context.Background(), source, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != StateFailed || failed.LeaseID != "" || failed.LastOutcome != RunFailed || failed.LastReason != "finalize_failed" {
		t.Fatalf("failed workspace = %+v", failed)
	}
	retry, err := manager.Acquire(context.Background(), failed)
	if err != nil {
		t.Fatalf("acquire after failed finalization: %v", err)
	}
	if _, err := manager.Complete(context.Background(), failed, retry, Author{Name: "Agent Bot", Email: "agent@example.test"}, RunSucceeded); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireRejectsSymlinkedRunLock(t *testing.T) {
	source := newSourceRepository(t)
	manager := NewManager(t.TempDir())
	created, err := manager.Prepare(context.Background(), PrepareRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(filepath.Dir(created.CheckoutPath), "run.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(context.Background(), created); err == nil || !strings.Contains(err.Error(), "run lock") {
		t.Fatalf("symlinked run lock error = %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "outside" {
		t.Fatalf("outside lock target = %q, %v", content, err)
	}
}

func newSourceRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.name", "Human")
	runGit(t, repo, "config", "user.email", "human@example.test")
	runGit(t, repo, "remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-q", "-m", "initial")
	root, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.Command("git", commandArgs...)
	command.Env = gitEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(commandArgs, " "), err, output)
	}
}

func gitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.Command("git", commandArgs...)
	command.Env = gitEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(commandArgs, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func assertOwnerOnlyDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("directory %s mode = %v, want owner-only", path, info.Mode())
	}
}

func shellVariable(name, value string) string {
	return name + "=" + shellQuoteTest(value)
}

func shellQuoteTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
