package control

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/control/plan"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

const plannerAgentsManifest = `{"schema_version":"ai-agent-manifest/v1","agents":{"allowed":["claude","codex"],"defaults":{"claude":{"model":"claude-sonnet-5"}}},"contracts":[{"name":"tests","command":"make test"},{"name":"lint","command":"make lint","retry":"never"}]}`

func TestPlannerBuildsValidDevcontainerRunPlan(t *testing.T) {
	repo := writePlannerRepo(t, plannerAgentsManifest, "https://github.com/owner/repo.git")
	home := t.TempDir()
	configDir := t.TempDir()
	runtimeDir := t.TempDir()
	helper := writeExecutable(t, t.TempDir(), "ai-agent-credential-helper")
	wrapper := writeExecutable(t, t.TempDir(), "ai-agent-gh")
	realGh := writeExecutable(t, t.TempDir(), "gh")

	t.Setenv("HOME", home)
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv(paths.EnvContainer, "1")
	t.Setenv(paths.EnvRealGh, realGh)
	writePlannerIdentity(t, configDir, "claude", "claude-code", "configured-model")

	var notes strings.Builder
	planned, err := NewPlanner(&notes).PlanRun(RunRequest{
		AgentName:             "claude",
		TaskRef:               "github:owner/repo#43",
		RepoPath:              repo,
		CredentialHelperPath:  helper,
		GhWrapperPath:         wrapper,
		MaxRetries:            2,
		IsolateHome:           true,
		AgentCommand:          []string{"claude"},
		ObservabilityResource: "langfuse:project:proj-1",
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}

	snapshot := planned.Plan.Snapshot()
	if snapshot.Repository.Slug != "owner/repo" || snapshot.Repository.RootPath == "" {
		t.Fatalf("repository = %+v", snapshot.Repository)
	}
	if snapshot.Agent.ConfiguredModel != "claude-sonnet-5" {
		t.Fatalf("configured model = %q, want manifest default", snapshot.Agent.ConfiguredModel)
	}
	if snapshot.Broker.SocketPath == "" || snapshot.Broker.HostRepoPath != snapshot.Repository.RootPath {
		t.Fatalf("broker = %+v", snapshot.Broker)
	}
	if len(snapshot.Broker.Resources) != 2 || snapshot.Broker.Resources[0].URI != "github:repo:owner/repo" || snapshot.Broker.Resources[1].URI != "langfuse:project:proj-1" {
		t.Fatalf("resources = %+v", snapshot.Broker.Resources)
	}
	if snapshot.Runtime.Network.Mode != "restricted" || !snapshot.Runtime.Network.FailClosedWhenAbsent {
		t.Fatalf("network = %+v", snapshot.Runtime.Network)
	}
	if snapshot.Env.CredentialHelperPath != helper || snapshot.Env.RealGhPath != realGh {
		t.Fatalf("env = %+v", snapshot.Env)
	}
	if snapshot.Home.SourceHome != home || !hasProjectedPath(snapshot.Home.ProjectedPaths, ".codex", "packages") {
		t.Fatalf("home = %+v", snapshot.Home)
	}
	if !snapshot.Telemetry.EventsRetainedLocally || len(snapshot.Telemetry.ObservabilitySinks) != 1 {
		t.Fatalf("telemetry = %+v", snapshot.Telemetry)
	}
	if len(snapshot.Quality.Contracts) != 2 || snapshot.Quality.Contracts[1].RetryAgent {
		t.Fatalf("quality = %+v", snapshot.Quality.Contracts)
	}
	if snapshot.RunID == "" || snapshot.Retry.MaxAgentRetries != 2 || !snapshot.Cleanup.CleanupHome {
		t.Fatalf("planned run lifecycle = run_id %q retry %+v cleanup %+v", snapshot.RunID, snapshot.Retry, snapshot.Cleanup)
	}
	if len(snapshot.Intercept.Wrappers) != 1 || snapshot.Intercept.Wrappers[0].Path != wrapper {
		t.Fatalf("interception = %+v", snapshot.Intercept)
	}
	if !strings.Contains(notes.String(), "project contract") || !strings.Contains(notes.String(), "project manifest default") {
		t.Fatalf("notes = %q", notes.String())
	}
}

func hasProjectedPath(paths []plan.ProjectedPath, name string, excluded string) bool {
	for _, path := range paths {
		if path.Name == name && path.Kind == plan.ProjectedPathDir && slices.Contains(path.Exclude, excluded) {
			return true
		}
	}
	return false
}

func TestPlannerRejectsNativeHostRunBeforeHelperResolution(t *testing.T) {
	repo := writePlannerRepo(t, plannerAgentsManifest, "https://github.com/owner/repo.git")
	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	writePlannerIdentity(t, configDir, "claude", "claude-code", "configured-model")

	_, err := NewPlanner(&strings.Builder{}).PlanRun(RunRequest{
		AgentName:            "claude",
		RepoPath:             repo,
		CredentialHelperPath: filepath.Join(t.TempDir(), "missing-helper"),
		MaxRetries:           2,
		IsolateHome:          true,
		AgentCommand:         []string{"claude"},
	})
	if err == nil || !strings.Contains(err.Error(), "devcontainer-only") {
		t.Fatalf("err = %v, want devcontainer boundary failure", err)
	}
	if strings.Contains(err.Error(), "credential helper") || strings.Contains(err.Error(), "broker") {
		t.Fatalf("err = %v, boundary failure must occur before helper or broker setup", err)
	}
}

func TestPlannerRejectsManifestToolMismatchBeforeDevcontainerBoundary(t *testing.T) {
	repo := writePlannerRepo(t, plannerAgentsManifest, "https://github.com/owner/repo.git")
	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	writePlannerIdentity(t, configDir, "claude", "claude-code", "configured-model")

	_, err := NewPlanner(&strings.Builder{}).PlanRun(RunRequest{
		AgentName:    "claude",
		RepoPath:     repo,
		MaxRetries:   2,
		IsolateHome:  true,
		AgentCommand: []string{"codex"},
	})
	if err == nil || !strings.Contains(err.Error(), `does not match configured tool "claude-code"`) {
		t.Fatalf("err = %v, want configured-tool failure", err)
	}
}

func TestPlannerRejectsSSHRemoteBeforeLauncherBridge(t *testing.T) {
	repo := writePlannerRepo(t, plannerAgentsManifest, "git@github.com:owner/repo.git")
	configDir := t.TempDir()
	helper := writeExecutable(t, t.TempDir(), "ai-agent-credential-helper")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(paths.EnvContainer, "1")
	writePlannerIdentity(t, configDir, "claude", "claude-code", "configured-model")

	_, err := NewPlanner(&strings.Builder{}).PlanRun(RunRequest{
		AgentName:            "claude",
		RepoPath:             repo,
		CredentialHelperPath: helper,
		MaxRetries:           2,
		IsolateHome:          true,
		AgentCommand:         []string{"claude"},
	})
	if err == nil || !strings.Contains(err.Error(), "uses an SSH remote") {
		t.Fatalf("err = %v, want SSH remote refusal", err)
	}
}

func TestPlannerRejectsInvalidObservabilityResource(t *testing.T) {
	repo := writePlannerRepo(t, plannerAgentsManifest, "https://github.com/owner/repo.git")
	configDir := t.TempDir()
	helper := writeExecutable(t, t.TempDir(), "ai-agent-credential-helper")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(paths.EnvContainer, "1")
	writePlannerIdentity(t, configDir, "claude", "claude-code", "configured-model")

	_, err := NewPlanner(&strings.Builder{}).PlanRun(RunRequest{
		AgentName:             "claude",
		RepoPath:              repo,
		CredentialHelperPath:  helper,
		MaxRetries:            2,
		IsolateHome:           true,
		AgentCommand:          []string{"claude"},
		ObservabilityResource: "github:repo:owner/repo",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid observability resource") {
		t.Fatalf("err = %v, want observability resource failure", err)
	}
}

func TestPlannerRejectsOutOfRangeRetryBudget(t *testing.T) {
	_, err := NewPlanner(&strings.Builder{}).PlanRun(RunRequest{
		AgentName:    "claude",
		RepoPath:     t.TempDir(),
		MaxRetries:   11,
		IsolateHome:  true,
		AgentCommand: []string{"claude"},
	})
	if err == nil || !strings.Contains(err.Error(), "--max-retries") {
		t.Fatalf("err = %v, want retry budget failure", err)
	}
}

func writePlannerRepo(t *testing.T, manifestContent string, remote string) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "agent@example.test")
	runGit(t, repo, "config", "user.name", "Agent")
	runGit(t, repo, "remote", "add", "origin", remote)
	if manifestContent != "" {
		if err := os.MkdirAll(filepath.Join(repo, ".ai-agent"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".ai-agent", "manifest.json"), []byte(manifestContent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func writePlannerIdentity(t *testing.T, configDir string, agentName string, tool string, model string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema_version":"ai-agent-identities/v2","agents":{"` + agentName + `":{"git_name":"` + agentName + `[bot]","git_email":"` + agentName + `@example.test","github_host":"github.com","app_id":"123","app_key":"/tmp/key.pem","tool":"` + tool + `","model":"` + model + `"}}}`)
	if err := os.WriteFile(filepath.Join(configDir, "identities.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, dir string, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
