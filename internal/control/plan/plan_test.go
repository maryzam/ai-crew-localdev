package plan

import (
	"strings"
	"testing"
)

func TestNewAcceptsCurrentManagedRunShape(t *testing.T) {
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
	if !snapshot.Cleanup.RevokeBrokerSession {
		t.Fatal("revoke broker session must be planned")
	}
	if !snapshot.Telemetry.EventsRetainedLocally {
		t.Fatal("local event retention must be planned")
	}
}

func TestValidateRejectsIncompleteSecurityFields(t *testing.T) {
	draft := validDraft()
	draft.Env.CredentialHelperPath = ""
	draft.Runtime.ExtraFiles = nil
	draft.Cleanup.RevokeBrokerSession = false
	draft.Telemetry.EventsRetainedLocally = false

	errs := Validate(draft)
	if !errs.HasErrors() {
		t.Fatal("expected validation errors")
	}

	for _, field := range []string{
		"env.credential_helper_path",
		"runtime.extra_files",
		"cleanup.revoke_broker_session",
		"telemetry.events_retained_locally",
	} {
		if !hasField(errs, field) {
			t.Fatalf("missing validation error for %s in %v", field, errs)
		}
	}
}

func TestValidateRejectsInvalidBudgetsAndModes(t *testing.T) {
	draft := validDraft()
	draft.Runtime.Mode = "sidecar"
	draft.Runtime.Network.Mode = "ambient"
	draft.Home.Mode = "shared"
	draft.Budgets = []Budget{{
		Name:       "tokens",
		Metric:     "requests",
		WarnAt:     100,
		StopAt:     50,
		StopPolicy: "ignore",
	}}

	errs := Validate(draft)
	for _, field := range []string{
		"runtime.mode",
		"runtime.network.mode",
		"home.mode",
		"budgets[0].metric",
		"budgets[0].stop_policy",
		"budgets[0].warn_at",
	} {
		if !hasField(errs, field) {
			t.Fatalf("missing validation error for %s in %v", field, errs)
		}
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
		URI:        "github:repo:maryzam/ai-crew-localdev",
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

func TestValidateRejectsIncompleteIsolatedHome(t *testing.T) {
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

	first := got.Snapshot()
	if first.Agent.Command[0] != "codex" {
		t.Fatalf("stored command changed through draft alias: %#v", first.Agent.Command)
	}
	if first.Broker.Resources[0].URI != "github:repo:maryzam/ai-crew-localdev" {
		t.Fatalf("stored resource changed through draft alias: %#v", first.Broker.Resources)
	}
	if first.Intercept.Profiles[0].Commands[0] != "gh" {
		t.Fatalf("stored profile changed through draft alias: %#v", first.Intercept.Profiles)
	}

	first.Agent.Command[0] = "changed"
	first.Broker.Resources[0].URI = "changed"
	first.Intercept.Profiles[0].Commands[0] = "changed"

	second := got.Snapshot()
	if second.Agent.Command[0] != "codex" {
		t.Fatalf("stored command changed through snapshot alias: %#v", second.Agent.Command)
	}
	if second.Broker.Resources[0].URI != "github:repo:maryzam/ai-crew-localdev" {
		t.Fatalf("stored resource changed through snapshot alias: %#v", second.Broker.Resources)
	}
	if second.Intercept.Profiles[0].Commands[0] != "gh" {
		t.Fatalf("stored profile changed through snapshot alias: %#v", second.Intercept.Profiles)
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

func validDraft() Draft {
	return Draft{
		RunID:   "run_0123456789abcdef0123456789abcdef",
		TaskRef: "github:maryzam/ai-crew-localdev#92",
		Repository: Repository{
			RootPath: "/workspaces/ai-crew-localdev",
			Slug:     "maryzam/ai-crew-localdev",
			Remote:   "https://github.com/maryzam/ai-crew-localdev.git",
		},
		Agent: Agent{
			Name:            "codex",
			Tool:            "codex",
			ConfiguredModel: "gpt-5.2-codex",
			Command:         []string{"codex", "exec", "make test"},
		},
		Broker: BrokerSession{
			SocketPath:   "/run/user/1000/ai-agent/broker.sock",
			AgentName:    "codex",
			HostRepoPath: "/workspaces/ai-crew-localdev",
			Resources: []ProviderResource{{
				URI:        "github:repo:maryzam/ai-crew-localdev",
				Provider:   "github",
				Kind:       "repo",
				Identifier: "maryzam/ai-crew-localdev",
			}},
		},
		Runtime: Runtime{
			Mode:    RuntimeModeNative,
			WorkDir: "/workspaces/ai-crew-localdev",
			Network: NetworkPolicy{
				Mode:                 NetworkModeHost,
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
			}},
			Wrappers: []CommandWrapper{{
				Provider: "github",
				Command:  "gh",
				Path:     "/usr/local/bin/ai-agent-gh",
			}},
		},
		Home: Home{
			Mode:           HomeModeIsolated,
			SourceHome:     "/home/mary",
			ProjectedPaths: []string{".codex"},
		},
		Telemetry: Telemetry{
			LocalHistoryPath:      "/home/mary/.local/state/ai-agent/runs.jsonl",
			AuditLogPath:          "/home/mary/.local/state/ai-agent/audit.jsonl",
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
				WorkDir:         "/workspaces/ai-crew-localdev",
				RetryAgent:      true,
				TailLines:       60,
				EvidenceDir:     "/home/mary/.config/ai-agent/evidence",
				EvidenceMaxRuns: 20,
			}},
		},
		Retry: Retry{
			MaxAgentRetries: 2,
		},
		Cleanup: Cleanup{
			RevokeBrokerSession: true,
			RemoveSessionInfo:   true,
			CleanupHome:         true,
		},
	}
}
