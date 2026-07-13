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

	var notes strings.Builder
	planned, err := NewPlanner(&notes).PlanRun(RunRequest{
		AgentName:             "claude",
		TaskRef:               "github:owner/repo#43",
		RepoPath:              repo,
		CredentialHelperPath:  helper,
		GhWrapperPath:         wrapper,
		MaxRetries:            2,
		TokenWarnAt:           1000,
		TokenStopAt:           1200,
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
	if snapshot.Agent.Type != "claude_code" || snapshot.Agent.CommandName != "claude" {
		t.Fatalf("agent attribution = %#v", snapshot.Agent)
	}
	if snapshot.Agent.Model.Provider != "anthropic" || snapshot.Agent.Model.Family != "claude-sonnet" || snapshot.Agent.Model.Requested != "claude-sonnet-5" {
		t.Fatalf("model attribution = %#v", snapshot.Agent.Model)
	}
	if snapshot.Agent.Model.Resolution.PrimarySource != "identity_config" {
		t.Fatalf("model resolution = %#v", snapshot.Agent.Model.Resolution)
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
	if len(snapshot.Quality.Contracts) != 2 || snapshot.Quality.Contracts[0].FailurePolicy != plan.QualityFailurePolicyRetryAgent || snapshot.Quality.Contracts[1].FailurePolicy != plan.QualityFailurePolicyFailRun {
		t.Fatalf("quality = %+v", snapshot.Quality.Contracts)
	}
	if snapshot.RunID == "" || snapshot.Retry.MaxAgentRetries != 2 || snapshot.Retry.Attempts() != 3 || !snapshot.Cleanup.CleanupHome {
		t.Fatalf("planned run lifecycle = run_id %q retry %+v cleanup %+v", snapshot.RunID, snapshot.Retry, snapshot.Cleanup)
	}
	if len(snapshot.Budgets) != 1 || snapshot.Budgets[0].Metric != plan.BudgetMetricTokens || snapshot.Budgets[0].MeasurementSource != plan.BudgetMeasurementSourceNativeOTEL || snapshot.Budgets[0].WarnAt != 1000 || snapshot.Budgets[0].StopAt != 1200 || snapshot.Budgets[0].StopPolicy != plan.BudgetStopPolicyStopRun {
		t.Fatalf("budgets = %+v", snapshot.Budgets)
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
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

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

func TestPlannerInsideContainerIgnoresLocalGovernanceCopies(t *testing.T) {
	repo := writePlannerRepo(t, plannerAgentsManifest, "https://github.com/owner/repo.git")
	helper := writeExecutable(t, t.TempDir(), "ai-agent-credential-helper")
	configDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(paths.EnvContainer, "1")
	if err := os.WriteFile(filepath.Join(configDir, "identities.json"), []byte(`{"schema_version":"ai-agent-identities/v2","agents":{"claude":{"git_name":"","git_email":""}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "policy.json"), []byte(`{"schema_version":"2","agents":{"claude":{"providers":{"github":{"installation_id":0}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	planned, err := NewPlanner(&strings.Builder{}).PlanRun(RunRequest{
		AgentName:            "claude",
		RepoPath:             repo,
		CredentialHelperPath: helper,
		MaxRetries:           2,
		IsolateHome:          true,
		AgentCommand:         []string{"claude"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	snapshot := planned.Plan.Snapshot()
	if snapshot.Agent.Tool != "claude-code" || snapshot.Agent.CommandName != "claude" {
		t.Fatalf("agent planning did not use compiled capability state: %#v", snapshot.Agent)
	}
	if len(snapshot.Broker.Resources) != 1 || snapshot.Broker.Resources[0].URI != "github:repo:owner/repo" {
		t.Fatalf("resources = %+v", snapshot.Broker.Resources)
	}
}

func TestPlannerRejectsSSHRemoteBeforeLauncherBridge(t *testing.T) {
	repo := writePlannerRepo(t, plannerAgentsManifest, "git@github.com:owner/repo.git")
	helper := writeExecutable(t, t.TempDir(), "ai-agent-credential-helper")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(paths.EnvContainer, "1")

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
	helper := writeExecutable(t, t.TempDir(), "ai-agent-credential-helper")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(paths.EnvContainer, "1")

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

func TestPlannerVerifyCommandPlansQualityContractShape(t *testing.T) {
	repo := writePlannerRepo(t, plannerAgentsManifest, "https://github.com/owner/repo.git")
	helper := writeExecutable(t, t.TempDir(), "ai-agent-credential-helper")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(paths.EnvContainer, "1")

	var notes strings.Builder
	planned, err := NewPlanner(&notes).PlanRun(RunRequest{
		AgentName:            "claude",
		RepoPath:             repo,
		CredentialHelperPath: helper,
		VerifyCommand:        "make verify",
		MaxRetries:           1,
		IsolateHome:          true,
		AgentCommand:         []string{"claude"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}

	snapshot := planned.Plan.Snapshot()
	if len(snapshot.Quality.Contracts) != 1 {
		t.Fatalf("quality = %+v", snapshot.Quality.Contracts)
	}
	contract := snapshot.Quality.Contracts[0]
	if contract.Name != "verify-cmd" || contract.Command != "make verify" || contract.WorkDir != snapshot.Repository.RootPath {
		t.Fatalf("contract = %+v", contract)
	}
	if contract.FailurePolicy != plan.QualityFailurePolicyRetryAgent || contract.TailLines != 60 || contract.EvidenceDir == "" || contract.EvidenceMaxRuns != 20 {
		t.Fatalf("contract budgets = %+v", contract)
	}
	if snapshot.Retry.MaxAgentRetries != 1 || snapshot.Retry.Attempts() != 2 {
		t.Fatalf("retry = %+v", snapshot.Retry)
	}
	if !strings.Contains(notes.String(), "--verify-cmd overrides") {
		t.Fatalf("notes = %q", notes.String())
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

func TestPlannerRejectsInvalidTokenBudget(t *testing.T) {
	_, err := NewPlanner(&strings.Builder{}).PlanRun(RunRequest{
		AgentName:    "claude",
		RepoPath:     t.TempDir(),
		MaxRetries:   2,
		TokenWarnAt:  20,
		TokenStopAt:  10,
		IsolateHome:  true,
		AgentCommand: []string{"claude"},
	})
	if err == nil || !strings.Contains(err.Error(), "--token-warn-at") {
		t.Fatalf("err = %v, want token budget validation failure", err)
	}
}

func TestPlannerRejectsTokenBudgetWithoutNativeTelemetry(t *testing.T) {
	repo := writePlannerRepo(t, "", "https://github.com/owner/repo.git")
	helper := writeExecutable(t, t.TempDir(), "ai-agent-credential-helper")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(paths.EnvContainer, "1")

	_, err := NewPlanner(&strings.Builder{}).PlanRun(RunRequest{
		AgentName:            "custom",
		RepoPath:             repo,
		CredentialHelperPath: helper,
		MaxRetries:           2,
		TokenStopAt:          100,
		IsolateHome:          true,
		AgentCommand:         []string{"custom-agent"},
	})
	if err == nil || !strings.Contains(err.Error(), "native telemetry support") {
		t.Fatalf("err = %v, want native telemetry budget refusal", err)
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
