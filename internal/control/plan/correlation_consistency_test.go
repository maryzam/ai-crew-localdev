package plan_test

import (
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/control/plan"
	"github.com/maryzam/ai-crew-localdev/internal/platform/correlation"
)

func TestPlanCorrelationValidationMatchesPlatformRules(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runID   string
		taskRef string
	}{
		{name: "empty task ref", runID: "run_123", taskRef: ""},
		{name: "max run id", runID: correlation.RunIDPrefix + strings.Repeat("a", correlation.MaxRunIDLength-len(correlation.RunIDPrefix)), taskRef: "github:example-org/example-repo#95"},
		{name: "max task ref", runID: "run_456", taskRef: strings.Repeat("a", correlation.MaxTaskRefLength)},
		{name: "missing run prefix", runID: "missing-prefix", taskRef: "github:example-org/example-repo#95"},
		{name: "empty run suffix", runID: correlation.RunIDPrefix, taskRef: "github:example-org/example-repo#95"},
		{name: "run too long", runID: correlation.RunIDPrefix + strings.Repeat("a", correlation.MaxRunIDLength), taskRef: "github:example-org/example-repo#95"},
		{name: "run whitespace", runID: "run_with space", taskRef: "github:example-org/example-repo#95"},
		{name: "task too long", runID: "run_789", taskRef: strings.Repeat("a", correlation.MaxTaskRefLength+1)},
		{name: "task whitespace", runID: "run_abc", taskRef: "github:example-org/example-repo#95 with-space"},
		{name: "task non ascii", runID: "run_def", taskRef: "github:example-org/example-répo#95"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := validDraft()
			draft.RunID = tc.runID
			draft.TaskRef = tc.taskRef

			platformValid := correlation.ValidateRunID(tc.runID) == nil && correlation.ValidateTaskRef(tc.taskRef) == nil
			planValid := !plan.Validate(draft).HasErrors()
			if planValid != platformValid {
				t.Fatalf("plan valid = %v, platform valid = %v", planValid, platformValid)
			}
		})
	}
}

func validDraft() plan.Draft {
	return plan.Draft{
		RunID:   "run_0123456789abcdef0123456789abcdef",
		TaskRef: "github:example-org/example-repo#95",
		Repository: plan.Repository{
			RootPath: "/workspaces/example-repo",
			Slug:     "example-org/example-repo",
			Remote:   "https://github.com/example-org/example-repo.git",
		},
		Agent: plan.Agent{
			Name:            "codex",
			Tool:            "codex",
			Type:            "codex",
			ConfiguredModel: "gpt-5.2-codex",
			CommandName:     "codex",
			Command:         []string{"codex", "exec", "make test"},
			Model: plan.ModelAttribution{
				Provider:  "openai",
				Family:    "gpt-5",
				Requested: "gpt-5.2-codex",
				Resolution: plan.ModelResolution{
					Status:        "resolved",
					Confidence:    "configured",
					PrimarySource: "identity_config",
					Sources:       []string{"identity_config"},
				},
			},
		},
		Broker: plan.BrokerSession{
			SocketPath:   "/run/user/1000/ai-agent/broker.sock",
			AgentName:    "codex",
			HostRepoPath: "/workspaces/example-repo",
			Resources: []plan.ProviderResource{{
				URI:        "github:repo:example-org/example-repo",
				Provider:   "github",
				Kind:       "repo",
				Identifier: "example-org/example-repo",
			}},
		},
		Runtime: plan.Runtime{
			WorkDir: "/workspaces/example-repo",
			Network: plan.NetworkPolicy{
				Mode:                 plan.NetworkModeRestricted,
				AllowedDestinations:  []string{"github.com"},
				FailClosedWhenAbsent: true,
			},
			ExtraFiles: []plan.ExtraFile{{Name: "session_bind", TargetFD: 3}},
		},
		Env: plan.Environment{
			CredentialHelperPath: "/usr/local/bin/ai-agent-credential-helper",
		},
		Intercept: plan.Interception{
			Profiles: []plan.InterceptionProfile{{
				Provider:      "github",
				Commands:      []string{"gh"},
				ScrubEnv:      []string{"GH_TOKEN"},
				FailClosedEnv: []plan.EnvironmentVariable{{Name: "GIT_TERMINAL_PROMPT", Value: "0"}},
			}},
		},
		Home: plan.Home{
			SourceHome:     "/home/example-agent",
			ProjectedPaths: []plan.ProjectedPath{{Name: ".codex", Kind: plan.ProjectedPathDir, Exclude: []string{"packages", "tmp"}}},
		},
		Telemetry: plan.Telemetry{
			LocalHistoryPath:      "/home/example-agent/.local/state/ai-agent/runs.jsonl",
			EventsRetainedLocally: true,
		},
		Cleanup: plan.Cleanup{
			RevokeBrokerSession: true,
			RemoveSessionInfo:   true,
			CleanupHome:         true,
		},
	}
}
