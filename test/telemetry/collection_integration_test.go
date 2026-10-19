//go:build integration

package telemetry

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

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

func requireManagedFilesReadOnly(t *testing.T, cli cliImage) {
	t.Helper()
	for path := range runtelemetry.ImageFiles() {
		if err := exec.Command("podman", "run", "--rm", "--network", "none", "--userns", "keep-id", "--entrypoint", "sh", cli.image, "-c", `printf x > "$1"`, "sh", path).Run(); err == nil {
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
				run := newTelemetryRun(t, cli, c.model, 0, true)
				spec := c.run(t, run)
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
			run := newTelemetryRun(t, cli, c.model, killHold, true)
			spec := c.run(t, run)
			run.executeAndKill(t, spec, killAfterExported)
			requireContent(t, spec.label, run.stored(t, spec.label+"-killed", "logs", "spans"), run.sentinels.byLabel(), "prompt", "tool input", "tool output")
		})
	}
}

func TestAgentKeepsWorkingWhenTheCollectorIsDownAndTheLossIsRecorded(t *testing.T) {
	cli := buildCLIImage(t, runtelemetry.Claude, pinnedClaudeVersion)
	run := newTelemetryRun(t, cli, claudeModel, 0, false)
	run.execute(t, claudeRun(t, run))
	run.finishStats()
	summary, err := runtelemetry.Summarize(run.root, run.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Forwarder == nil || summary.Forwarder.Failed == 0 || summary.Forwarder.Forwarded != 0 {
		t.Fatalf("lost telemetry is not recorded: %+v", summary.Forwarder)
	}
	if summary.TelemetryBytes != 0 {
		t.Fatalf("telemetry stored without a collector: %d bytes", summary.TelemetryBytes)
	}
}

func containerID(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("podman", "container", "inspect", "--format", "{{.Id}}", name).Output()
	if err != nil {
		t.Fatalf("inspect %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

func TestCollectorIsReusedUntilItsConfigurationChanges(t *testing.T) {
	first := startCollector(t, newRoot(t))
	id := containerID(t, first.Name)
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if containerID(t, first.Name) != id {
		t.Fatal("an unchanged collector was replaced")
	}
	moved := first
	moved.Root = newRoot(t)
	if err := moved.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if containerID(t, moved.Name) == id {
		t.Fatal("a collector with a different evidence root was reused")
	}
}

func TestUnstampedTelemetryIsKeptAndOversizedRequestsAreRefused(t *testing.T) {
	root := newRoot(t)
	collector := startCollector(t, root)
	direct := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", collector.Socket())
	}}}
	resp, err := direct.Post("http://collector/v1/logs", "application/json", strings.NewReader(`{"resourceLogs":[{"resource":{"attributes":[{"key":"ai.run.id","value":{"stringValue":"../escape"}}]},"scopeLogs":[{"logRecords":[{"body":{"stringValue":"unstamped"}}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("collector refused an unstamped record: %d", resp.StatusCode)
	}

	forwarder, err := runtelemetry.NewForwarder(runtelemetry.Context{RunID: "run-oversized", Task: "t", Project: "p", ImageDigest: "sha256:x", EgressMode: "none", Agents: map[runtelemetry.Agent]string{runtelemetry.Claude: "1"}}, collector.Socket())
	if err != nil {
		t.Fatal(err)
	}
	socket := shortTempDir(t, "nf") + "/run.sock"
	serveUnix(t, socket, forwarder)
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}}
	for path, want := range map[string]int{"/otlp/claude/v1/logs": http.StatusRequestEntityTooLarge, "/otlp/gemini/v1/logs": http.StatusNotFound} {
		body := strings.NewReader(strings.Repeat("x", runtelemetry.MaxRequestBytes+1))
		resp, err := client.Post("http://run"+path, "application/json", body)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: status %d, want %d", path, resp.StatusCode, want)
		}
	}
	if s := forwarder.Stats(); s.Rejected != 2 || s.Forwarded != 0 || s.Failed != 0 {
		t.Errorf("refusals not counted: %+v", s)
	}

	if err := collector.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	summary, err := runtelemetry.Summarize(root, runtelemetry.UnattributedRun)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Records["logs"] != 1 {
		t.Fatalf("unstamped record not kept under %s: %v", runtelemetry.UnattributedRun, summary.Records)
	}
}
