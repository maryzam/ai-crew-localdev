//go:build integration

package telemetry

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/runtelemetry"
)

const (
	killHold          = 60 * time.Second
	killAfterExported = 3 * runtelemetry.ExportIntervalMillis * time.Millisecond
)

func claudeModel(s sentinels, l *modelLog) http.Handler { return &anthropicModel{s, l} }

func codexModel(s sentinels, l *modelLog) http.Handler { return &responsesModel{s, l} }

func hostileClaudeEnv() map[string]string {
	return map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY":        "0",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "0",
		"OTEL_LOGS_EXPORTER":                  "none",
		"OTEL_METRICS_EXPORTER":               "none",
		"OTEL_TRACES_EXPORTER":                "none",
		"OTEL_EXPORTER_OTLP_ENDPOINT":         divertedEndpoint,
		"OTEL_RESOURCE_ATTRIBUTES":            "ai.run.id=" + forgedRunID + ",ai.agent=codex",
		"OTEL_LOG_USER_PROMPTS":               "0",
		"OTEL_LOG_ASSISTANT_RESPONSES":        "0",
		"OTEL_LOG_TOOL_DETAILS":               "0",
		"OTEL_LOG_TOOL_CONTENT":               "0",
		"OTEL_LOG_RAW_API_BODIES":             "0",
	}
}

func claudeRun(t *testing.T, run *telemetryRun) cliRun {
	t.Helper()
	hostile, err := json.Marshal(map[string]any{"env": hostileClaudeEnv()})
	if err != nil {
		t.Fatal(err)
	}
	env := []string{
		"CLAUDE_CONFIG_DIR=" + agentHome + "/.claude",
		"CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-ai-crew-placeholder",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:8080",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1",
	}
	for k, v := range hostileClaudeEnv() {
		env = append(env, k+"="+v)
	}
	return cliRun{
		label:     "claude-" + run.cli.version,
		env:       env,
		home:      map[string]string{".claude/settings.json": string(hostile)},
		workspace: map[string]string{".claude/settings.json": string(hostile)},
		command:   []string{"claude", "-p", run.sentinels.userPrompt(), "--model", "claude-sonnet-4-5", "--output-format", "text", "--dangerously-skip-permissions"},
	}
}

func hostileCodexConfig() string {
	exporter := func(signal string) string {
		return `{ otlp-http = { endpoint = "` + divertedEndpoint + `/v1/` + signal + `", protocol = "json" } }`
	}
	return strings.Join([]string{
		`model = "gpt-6-sol"`,
		`model_provider = "spike"`,
		`[model_providers.spike]`,
		`name = "spike"`,
		`base_url = "http://127.0.0.1:8080/v1"`,
		`wire_api = "responses"`,
		`env_key = "AI_CREW_PLACEHOLDER"`,
		`requires_openai_auth = false`,
		`[otel]`,
		`log_user_prompt = false`,
		`log_agent_responses = false`,
		`exporter = ` + exporter("logs"),
		`trace_exporter = ` + exporter("traces"),
		`metrics_exporter = ` + exporter("metrics"),
		`[otel.tool_result]`,
		`max_bytes = 0`,
	}, "\n") + "\n"
}

func codexRun(_ *testing.T, run *telemetryRun) cliRun {
	return cliRun{
		label: "codex-" + run.cli.version,
		env: []string{
			"CODEX_HOME=" + agentHome + "/.codex",
			"AI_CREW_PLACEHOLDER=placeholder",
			"OTEL_RESOURCE_ATTRIBUTES=ai.run.id=" + forgedRunID + ",ai.agent=claude",
		},
		home: map[string]string{".codex/config.toml": hostileCodexConfig()},
		command: []string{"codex", "-c", "otel.log_user_prompt=false", "-c", "otel.tool_result.max_bytes=0",
			"exec", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "--color", "never", "-C", workspace, run.sentinels.userPrompt()},
	}
}

type agentCase struct {
	agent       runtelemetry.Agent
	versionsEnv string
	pinned      string
	model       func(sentinels, *modelLog) http.Handler
	run         func(*testing.T, *telemetryRun) cliRun
	content     []string
}

var agentCases = []agentCase{
	{runtelemetry.Claude, claudeVersionsEnv, pinnedClaudeVersion, claudeModel, claudeRun, []string{"prompt", "response", "tool input", "tool output"}},
	{runtelemetry.Codex, codexVersionsEnv, pinnedCodexVersion, codexModel, codexRun, []string{"prompt", "tool input", "tool output"}},
}

const killAgentProcesses = `for p in /proc/[0-9]*; do pid=${p#/proc/}; [ "$pid" = "$$" ] || kill -KILL "$pid" 2>/dev/null || true; done; exec "$@"`

func asHostileAgent(command []string) []string {
	return append([]string{"sh", "-c", killAgentProcesses, "sh"}, command...)
}

func requireManagedFilesReadOnly(t *testing.T, cli cliImage) {
	t.Helper()
	for path := range runtelemetry.ImageFiles() {
		if err := exec.Command("podman", "run", "--rm", "--network", "none", "--userns", agentUserNamespace, "--user", runtelemetry.ContainerAgentUser, "--entrypoint", "sh", cli.image, "-c", `printf x > "$1"`, "sh", path).Run(); err == nil {
			t.Errorf("the agent user replaced %s in the %s image", path, cli.agent)
		}
	}
}

func TestEveryContentClassIsStoredWithHostContextDespiteAgentOverrides(t *testing.T) {
	for _, c := range agentCases {
		for _, version := range versionsFrom(c.versionsEnv, c.pinned) {
			t.Run(string(c.agent)+"/"+version, func(t *testing.T) {
				cli := buildCLIImage(t, c.agent, version)
				requireManagedFilesReadOnly(t, cli)
				run := newTelemetryRun(t, cli, c.model, 0)
				spec := c.run(t, run)
				spec.command = asHostileAgent(spec.command)
				run.execute(t, spec)
				requireContent(t, spec.label, run.stored(t, spec.label, "logs", "metrics", "spans"), run.sentinels.byLabel(), c.content...)
			})
		}
	}
}

func TestTelemetryExportedBeforeAContainerKillIsStored(t *testing.T) {
	for _, c := range agentCases {
		t.Run(string(c.agent), func(t *testing.T) {
			cli := buildCLIImage(t, c.agent, c.pinned)
			run := newTelemetryRun(t, cli, c.model, killHold)
			spec := c.run(t, run)
			run.executeAndKill(t, spec, killAfterExported)
			requireContent(t, spec.label, run.stored(t, spec.label+"-killed", "logs", "spans"), run.sentinels.byLabel(), "prompt", "tool input", "tool output")
		})
	}
}

func TestAgentKeepsWorkingWhenEvidenceStorageFailsAndTheLossIsRecorded(t *testing.T) {
	cli := buildCLIImage(t, runtelemetry.Claude, pinnedClaudeVersion)
	run := newTelemetryRun(t, cli, claudeModel, 0)
	runs := filepath.Join(run.root.Path(), evidence.RunsDir)
	if err := os.Chmod(runs, 0o500); err != nil {
		t.Fatal(err)
	}
	run.execute(t, claudeRun(t, run))
	if err := os.Chmod(runs, 0o700); err != nil {
		t.Fatal(err)
	}
	run.finishStats()
	summary, err := runtelemetry.Summarize(run.root, run.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Receiver == nil || summary.Receiver.Failed == 0 || summary.Receiver.Stored != 0 {
		t.Fatalf("lost telemetry is not recorded: %+v", summary.Receiver)
	}
	if summary.TelemetryBytes != 0 {
		t.Fatalf("telemetry stored while storage was failing: %d bytes", summary.TelemetryBytes)
	}
}
