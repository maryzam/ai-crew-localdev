package plan

import (
	"strings"
	"testing"
)

func TestNewAcceptsManagedDevcontainerRunShape(t *testing.T) {
	draft := validDraft()

	got, err := New(draft)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	snapshot := got.Snapshot()
	if snapshot.RunID != draft.RunID {
		t.Fatalf("run id = %q, want %q", snapshot.RunID, draft.RunID)
	}
	if snapshot.Agent.Command[0] != "codex" {
		t.Fatalf("agent command = %#v", snapshot.Agent.Command)
	}
	if snapshot.Agent.Type != "codex" || snapshot.Agent.CommandName != "codex" || snapshot.Agent.Model.Resolution.Status != "resolved" {
		t.Fatalf("agent attribution = %#v", snapshot.Agent)
	}
	if !snapshot.Cleanup.RevokeBrokerSession {
		t.Fatal("revoke broker session must be planned")
	}
	if !snapshot.Telemetry.EventsRetainedLocally {
		t.Fatal("local event retention must be planned")
	}
}

func TestValidateRejectsIncompleteSecurityFields(t *testing.T) {
	draft := validDraft()
	draft.Agent.Type = ""
	draft.Agent.CommandName = ""
	draft.Agent.Model.Resolution.Status = ""
	draft.Env.CredentialHelperPath = ""
	draft.Runtime.ExtraFiles = nil
	draft.Cleanup.RevokeBrokerSession = false
	draft.Telemetry.EventsRetainedLocally = false
	draft.Quality.Contracts[0].FailurePolicy = ""
	draft.Quality.Contracts[0].TailLines = 0
	draft.Quality.Contracts[0].EvidenceDir = ""
	draft.Quality.Contracts[0].EvidenceMaxRuns = 0
	draft.Retry.MaxAttempts = 0

	errs := Validate(draft)
	if !errs.HasErrors() {
		t.Fatal("expected validation errors")
	}

	for _, field := range []string{
		"env.credential_helper_path",
		"agent.type",
		"agent.command_name",
		"agent.model.resolution.status",
		"runtime.extra_files",
		"cleanup.revoke_broker_session",
		"telemetry.events_retained_locally",
		"quality.contracts[0].failure_policy",
		"quality.contracts[0].tail_lines",
		"quality.contracts[0].evidence_dir",
		"quality.contracts[0].evidence_max_runs",
		"retry.max_attempts",
	} {
		if !hasField(errs, field) {
			t.Fatalf("missing validation error for %s in %v", field, errs)
		}
	}
}

func TestValidateRejectsInvalidCorrelationFields(t *testing.T) {
	draft := validDraft()
	draft.RunID = "not-a-run-id"
	draft.TaskRef = "github:example-org/example-repo#92 with-space"

	errs := Validate(draft)
	for _, field := range []string{
		"run_id",
		"task_ref",
	} {
		if !hasField(errs, field) {
			t.Fatalf("missing validation error for %s in %v", field, errs)
		}
	}
}

func TestValidateRejectsInvalidBudgetsAndNetworkMode(t *testing.T) {
	draft := validDraft()
	draft.Runtime.Network.Mode = "ambient"
	draft.Budgets = []Budget{{
		Name:       "tokens",
		Metric:     "requests",
		WarnAt:     100,
		StopAt:     50,
		StopPolicy: "ignore",
	}}

	errs := Validate(draft)
	for _, field := range []string{
		"runtime.network.mode",
		"budgets[0].metric",
		"budgets[0].stop_policy",
		"budgets[0].warn_at",
	} {
		if !hasField(errs, field) {
			t.Fatalf("missing validation error for %s in %v", field, errs)
		}
	}
}

func TestValidateRejectsInconsistentRetryBudget(t *testing.T) {
	draft := validDraft()
	draft.Retry.MaxAgentRetries = 2
	draft.Retry.MaxAttempts = 2

	errs := Validate(draft)
	if !hasField(errs, "retry.max_attempts") {
		t.Fatalf("missing validation error for retry.max_attempts in %v", errs)
	}
}

func TestValidateRejectsIncompleteNetworkPolicy(t *testing.T) {
	draft := validDraft()
	draft.Runtime.Network.Mode = NetworkModeRestricted
	draft.Runtime.Network.AllowedDestinations = nil
	draft.Runtime.Network.FailClosedWhenAbsent = false

	errs := Validate(draft)
	for _, field := range []string{
		"runtime.network.fail_closed_when_absent",
		"runtime.network.allowed_destinations",
	} {
		if !hasField(errs, field) {
			t.Fatalf("missing validation error for %s in %v", field, errs)
		}
	}
}

func TestValidateRejectsResourceFieldDrift(t *testing.T) {
	draft := validDraft()
	draft.Broker.Resources[0] = ProviderResource{
		URI:        "github:repo:example-org/example-repo",
		Provider:   "langfuse",
		Kind:       "project",
		Identifier: "other",
	}

	errs := Validate(draft)
	for _, field := range []string{
		"broker.resources[0].provider",
		"broker.resources[0].kind",
		"broker.resources[0].identifier",
	} {
		if !hasField(errs, field) {
			t.Fatalf("missing validation error for %s in %v", field, errs)
		}
	}
}

func TestValidateRejectsInconsistentBrokerSession(t *testing.T) {
	draft := validDraft()
	draft.Broker.AgentName = "claude"
	draft.Broker.HostRepoPath = "/tmp/other"

	errs := Validate(draft)
	for _, field := range []string{
		"broker.agent_name",
		"broker.host_repo_path",
	} {
		if !hasField(errs, field) {
			t.Fatalf("missing validation error for %s in %v", field, errs)
		}
	}
}

func TestValidateRejectsIncompleteHomeProjection(t *testing.T) {
	draft := validDraft()
	draft.Home.SourceHome = ""
	draft.Home.ProjectedPaths = nil

	errs := Validate(draft)
	for _, field := range []string{
		"home.source_home",
		"home.projected_paths",
	} {
		if !hasField(errs, field) {
			t.Fatalf("missing validation error for %s in %v", field, errs)
		}
	}
}

func TestValidateRejectsUnsafeHomeProjection(t *testing.T) {
	for _, path := range []ProjectedPath{
		{Name: ".ssh", Kind: ProjectedPathDir},
		{Name: ".config/gh", Kind: ProjectedPathDir},
		{Name: ".codex", Kind: "other"},
		{Name: ".codex", Kind: ProjectedPathDir, Exclude: []string{"../packages"}},
	} {
		t.Run(path.Name, func(t *testing.T) {
			draft := validDraft()
			draft.Home.ProjectedPaths = []ProjectedPath{path}

			errs := Validate(draft)
			if !hasFieldPrefix(errs, "home.projected_paths[0]") {
				t.Fatalf("missing projected path validation error in %v", errs)
			}
		})
	}
}

func TestValidateRejectsStopBudgetWithoutThreshold(t *testing.T) {
	draft := validDraft()
	draft.Budgets[0].StopAt = 0

	errs := Validate(draft)
	if !hasField(errs, "budgets[0].stop_at") {
		t.Fatalf("missing validation error for stop budget threshold in %v", errs)
	}
}

func TestRunPlanSnapshotIsDeepCopied(t *testing.T) {
	draft := validDraft()
	got, err := New(draft)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	draft.Agent.Command[0] = "changed"
	draft.Broker.Resources[0].URI = "changed"
	draft.Intercept.Profiles[0].Commands[0] = "changed"
	draft.Intercept.Profiles[0].ScrubEnv[0] = "changed"
	draft.Intercept.Profiles[0].FailClosedEnv[0].Value = "changed"
	draft.Home.ProjectedPaths[0].Exclude[0] = "changed"

	first := got.Snapshot()
	if first.Agent.Command[0] != "codex" {
		t.Fatalf("stored command changed through draft alias: %#v", first.Agent.Command)
	}
	if first.Broker.Resources[0].URI != "github:repo:example-org/example-repo" {
		t.Fatalf("stored resource changed through draft alias: %#v", first.Broker.Resources)
	}
	if first.Intercept.Profiles[0].Commands[0] != "gh" {
		t.Fatalf("stored profile changed through draft alias: %#v", first.Intercept.Profiles)
	}
	if first.Intercept.Profiles[0].ScrubEnv[0] != "GH_TOKEN" || first.Intercept.Profiles[0].FailClosedEnv[0].Value != "0" {
		t.Fatalf("stored scrub profile changed through draft alias: %#v", first.Intercept.Profiles)
	}
	if first.Home.ProjectedPaths[0].Exclude[0] != "packages" {
		t.Fatalf("stored projected path changed through draft alias: %#v", first.Home.ProjectedPaths)
	}

	first.Agent.Command[0] = "changed"
	first.Broker.Resources[0].URI = "changed"
	first.Intercept.Profiles[0].Commands[0] = "changed"
	first.Intercept.Profiles[0].ScrubEnv[0] = "changed"
	first.Intercept.Profiles[0].FailClosedEnv[0].Value = "changed"
	first.Home.ProjectedPaths[0].Exclude[0] = "changed"

	second := got.Snapshot()
	if second.Agent.Command[0] != "codex" {
		t.Fatalf("stored command changed through snapshot alias: %#v", second.Agent.Command)
	}
	if second.Broker.Resources[0].URI != "github:repo:example-org/example-repo" {
		t.Fatalf("stored resource changed through snapshot alias: %#v", second.Broker.Resources)
	}
	if second.Intercept.Profiles[0].Commands[0] != "gh" {
		t.Fatalf("stored profile changed through snapshot alias: %#v", second.Intercept.Profiles)
	}
	if second.Intercept.Profiles[0].ScrubEnv[0] != "GH_TOKEN" || second.Intercept.Profiles[0].FailClosedEnv[0].Value != "0" {
		t.Fatalf("stored scrub profile changed through snapshot alias: %#v", second.Intercept.Profiles)
	}
	if second.Home.ProjectedPaths[0].Exclude[0] != "packages" {
		t.Fatalf("stored projected path changed through snapshot alias: %#v", second.Home.ProjectedPaths)
	}
}

func TestValidationErrorsError(t *testing.T) {
	errs := ValidationErrors{
		{Field: "one", Message: "first"},
		{Field: "two", Message: "second"},
	}

	got := errs.Error()
	if !strings.Contains(got, "one: first") || !strings.Contains(got, "two: second") {
		t.Fatalf("Error() = %q", got)
	}
}

func hasField(errs ValidationErrors, field string) bool {
	for _, err := range errs {
		if err.Field == field {
			return true
		}
	}
	return false
}

func hasFieldPrefix(errs ValidationErrors, prefix string) bool {
	for _, err := range errs {
		if strings.HasPrefix(err.Field, prefix) {
			return true
		}
	}
	return false
}

func validDraft() Draft {
	return Draft{
		RunID:   "run_0123456789abcdef0123456789abcdef",
		TaskRef: "github:example-org/example-repo#92",
		Repository: Repository{
			RootPath: "/workspaces/example-repo",
			Slug:     "example-org/example-repo",
			Remote:   "https://github.com/example-org/example-repo.git",
		},
		Agent: Agent{
			Name:            "codex",
			Tool:            "codex",
			Type:            "codex",
			ConfiguredModel: "gpt-5.2-codex",
			CommandName:     "codex",
			Command:         []string{"codex", "exec", "make test"},
			Model: ModelAttribution{
				Provider:  "openai",
				Family:    "gpt-5",
				Requested: "gpt-5.2-codex",
				Resolution: ModelResolution{
					Status:        "resolved",
					Confidence:    "configured",
					PrimarySource: "identity_config",
					Sources:       []string{"identity_config"},
				},
			},
		},
		Broker: BrokerSession{
			SocketPath:   "/run/user/1000/ai-agent/broker.sock",
			AgentName:    "codex",
			HostRepoPath: "/workspaces/example-repo",
			Resources: []ProviderResource{{
				URI:        "github:repo:example-org/example-repo",
				Provider:   "github",
				Kind:       "repo",
				Identifier: "example-org/example-repo",
			}},
		},
		Runtime: Runtime{
			WorkDir: "/workspaces/example-repo",
			Network: NetworkPolicy{
				Mode:                 NetworkModeRestricted,
				AllowedDestinations:  []string{"github.com"},
				FailClosedWhenAbsent: true,
			},
			ExtraFiles: []ExtraFile{{
				Name:     "session_bind",
				TargetFD: 3,
			}},
		},
		Env: Environment{
			CredentialHelperPath: "/usr/local/bin/ai-agent-credential-helper",
			Variables: []EnvironmentVariable{{
				Name:  "AI_AGENT_RUN_ID",
				Value: "run_0123456789abcdef0123456789abcdef",
			}},
		},
		Intercept: Interception{
			Profiles: []InterceptionProfile{{
				Provider: "github",
				Commands: []string{
					"gh",
				},
				ScrubEnv:         []string{"GH_TOKEN"},
				ScrubEnvPrefixes: []string{"GIT_CONFIG_KEY_"},
				FailClosedEnv:    []EnvironmentVariable{{Name: "GIT_TERMINAL_PROMPT", Value: "0"}},
			}},
			Wrappers: []CommandWrapper{{
				Provider: "github",
				Command:  "gh",
				Path:     "/usr/local/bin/ai-agent-gh",
			}},
		},
		Home: Home{
			SourceHome:     "/home/example-agent",
			ProjectedPaths: []ProjectedPath{{Name: ".codex", Kind: ProjectedPathDir, Exclude: []string{"packages", "tmp"}}},
		},
		Telemetry: Telemetry{
			LocalHistoryPath:      "/home/example-agent/.local/state/ai-agent/runs.jsonl",
			AuditLogPath:          "/home/example-agent/.local/state/ai-agent/audit.jsonl",
			NativeRelay:           true,
			EventsRetainedLocally: true,
		},
		Budgets: []Budget{{
			Name:       "tokens",
			Metric:     BudgetMetricTokens,
			WarnAt:     100000,
			StopAt:     120000,
			StopPolicy: BudgetStopPolicyStopRun,
		}},
		Quality: Quality{
			Contracts: []QualityContract{{
				Name:            "tests",
				Command:         "make test",
				WorkDir:         "/workspaces/example-repo",
				FailurePolicy:   QualityFailurePolicyRetryAgent,
				TailLines:       60,
				EvidenceDir:     "/home/example-agent/.config/ai-agent/evidence",
				EvidenceMaxRuns: 20,
			}},
		},
		Retry: Retry{
			MaxAgentRetries: 2,
			MaxAttempts:     3,
		},
		Cleanup: Cleanup{
			RevokeBrokerSession: true,
			RemoveSessionInfo:   true,
			CleanupHome:         true,
		},
	}
}
