package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
)

func TestLaunchTelemetryInvariantEveryManagedPathTerminatesOnce(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		verify      string
		createError bool
		revokeError bool
		outcome     string
	}{
		{name: "success", command: "true", outcome: telemetry.OutcomePassed},
		{name: "agent failure", command: "false", outcome: telemetry.OutcomeAgentFailed},
		{name: "agent missing", command: "definitely-not-a-real-binary", outcome: telemetry.OutcomeLaunchFailed},
		{name: "verification success", command: "true", verify: "true", outcome: telemetry.OutcomePassed},
		{name: "verification failure", command: "true", verify: "false", outcome: telemetry.OutcomeVerifyFailed},
		{name: "verification interrupted", command: "true", verify: "kill -TERM $$", outcome: telemetry.OutcomeInterrupted},
		{name: "revoke failure is visible", command: "true", revokeError: true, outcome: telemetry.OutcomePassed},
		{name: "broker failure", command: "true", createError: true, outcome: telemetry.OutcomeSessionCreateFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoDir := t.TempDir()
			configDir := t.TempDir()
			logPath := filepath.Join(configDir, "runs.jsonl")
			t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
			t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
			t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
			for _, key := range []string{"AI_AGENT_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT", "AI_AGENT_LANGFUSE_PUBLIC_KEY", "LANGFUSE_PUBLIC_KEY", "AI_AGENT_LANGFUSE_SECRET_KEY", "LANGFUSE_SECRET_KEY"} {
				t.Setenv(key, "")
			}
			runGit(t, repoDir, "init")
			runGit(t, repoDir, "remote", "add", "origin", "https://github.com/owner/repo.git")

			originalClient := newBrokerClient
			t.Cleanup(func() { newBrokerClient = originalClient })
			client := &stubBrokerClient{createResp: &brokerapi.CreateSessionResponse{
				SessionID: "sess-invariant", BindSecret: []byte("bind-secret"), ExpiresAt: time.Now().Add(time.Hour),
			}}
			if test.createError {
				client.createErr = errors.New("broker unavailable")
			}
			if test.revokeError {
				client.revokeErr = errors.New("revoke unavailable")
			}
			newBrokerClient = func(string) brokerClient { return client }

			_ = Launch(Options{
				AgentName: "codex", TaskRef: "github:owner/repo#43", RepoPath: repoDir,
				SocketPath: "/unused.sock", CredHelper: "/bin/true", AgentCommand: []string{test.command}, VerifyCmd: test.verify,
			})

			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read telemetry: %v", err)
			}
			if count := strings.Count(string(data), `"event_type":"run.finished"`); count != 1 {
				t.Fatalf("terminal event count = %d, want 1\n%s", count, data)
			}
			runs, err := telemetry.ReadRunHistory(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != 1 || runs[0].Outcome != test.outcome || runs[0].EndedAt == nil {
				t.Fatalf("run summary = %#v", runs)
			}
			if runs[0].Task.Ref != "github:owner/repo#43" {
				t.Fatalf("task ref = %q", runs[0].Task.Ref)
			}
			if test.name == "verification interrupted" && runs[0].Signal != "terminated" {
				t.Fatalf("signal = %q", runs[0].Signal)
			}
			if test.revokeError && (runs[0].Broker.SessionRevoked || runs[0].Diagnostics.ErrorType != "session_revoke_failed") {
				t.Fatalf("revoke failure summary = %#v", runs[0])
			}
			if got := client.createReqs[0].TaskRef; got != "github:owner/repo#43" {
				t.Fatalf("broker task ref = %q", got)
			}
		})
	}
}
