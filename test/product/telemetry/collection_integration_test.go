//go:build integration

package blackbox

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/product/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/product/telemetry"
	"github.com/maryzam/ai-crew-localdev/internal/product/wirecapture"
)

const (
	placeholderCredential = "sk-ant-oat01-ai-crew-placeholder-credential"
	killHold              = 60 * time.Second
	killAfterExported     = 3 * telemetry.ExportIntervalMillis * time.Millisecond
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
		"OTEL_LOG_RAW_API_BODIES":             "1",
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
		"CLAUDE_CODE_OAUTH_TOKEN=" + placeholderCredential,
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
			"AI_CREW_PLACEHOLDER=" + placeholderCredential,
			"OTEL_RESOURCE_ATTRIBUTES=ai.run.id=" + forgedRunID + ",ai.agent=claude",
		},
		home: map[string]string{".codex/config.toml": hostileCodexConfig()},
		command: []string{"codex", "-c", "otel.log_user_prompt=false", "-c", "otel.tool_result.max_bytes=0",
			"exec", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "--color", "never", "-C", workspace, run.sentinels.userPrompt()},
	}
}

type agentCase struct {
	agent       string
	versionsEnv string
	pinned      string
	model       func(sentinels, *modelLog) http.Handler
	run         func(*testing.T, *telemetryRun) cliRun
	content     []string
	wireKeys    []string
}

var agentCases = []agentCase{
	{"claude", claudeVersionsEnv, pinnedClaudeVersion, claudeModel, claudeRun, []string{"prompt", "response", "tool input", "tool output"}, []string{"messages", "system", "tools", "cache_control"}},
	{"codex", codexVersionsEnv, pinnedCodexVersion, codexModel, codexRun, []string{"prompt", "tool input", "tool output"}, []string{"input", "model", "tools"}},
}

const killAgentProcesses = `for p in /proc/[0-9]*; do pid=${p#/proc/}; [ "$pid" = "$$" ] || kill -KILL "$pid" 2>/dev/null || true; done; exec "$@"`

func asHostileAgent(command []string) []string {
	return append([]string{"sh", "-c", killAgentProcesses, "sh"}, command...)
}

func requireManagedFilesReadOnly(t *testing.T, cli cliImage) {
	t.Helper()
	for path := range telemetry.ImageFiles() {
		if err := exec.Command("podman", "run", "--rm", "--network", "none", "--userns", agentUserNamespace, "--user", telemetry.ContainerAgentUser, "--entrypoint", "sh", cli.image, "-c", `printf x > "$1"`, "sh", path).Run(); err == nil {
			t.Errorf("the agent user replaced %s in the %s image", path, cli.agent)
		}
	}
}

func TestEveryContentClassIsStoredWithHostContextDespiteAgentOverrides(t *testing.T) {
	for _, c := range agentCases {
		for _, version := range versionsFrom(c.versionsEnv, c.pinned) {
			t.Run(c.agent+"/"+version, func(t *testing.T) {
				cli := buildCLIImage(t, c.agent, version)
				requireManagedFilesReadOnly(t, cli)
				run := newTelemetryRun(t, cli, c.model, 0)
				spec := c.run(t, run)
				spec.command = asHostileAgent(spec.command)
				run.execute(t, spec)
				lines := run.stored(t, spec.label, "logs", "metrics", "spans")
				requireContent(t, spec.label, lines, run.sentinels.byLabel(), c.content...)
				if c.agent == "claude" {
					requireNoInlineBodies(t, spec.label, lines)
				}
				requireUsage(t, spec.label, run)
				requireExactWireCapture(t, spec.label, run, c.wireKeys)
			})
		}
	}
}

func requireNoInlineBodies(t *testing.T, label string, lines [][]byte) {
	t.Helper()
	for _, line := range lines {
		if bytes.Contains(line, []byte("api_request_body")) || bytes.Contains(line, []byte("api_response_body")) {
			t.Fatalf("%s stored inline API bodies despite the managed setting", label)
		}
	}
}

func requireUsage(t *testing.T, label string, run *telemetryRun) {
	t.Helper()
	summary, err := telemetry.Summarize(run.root, run.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Usage) == 0 {
		t.Fatalf("%s produced no usage rows from the genuine CLI", label)
	}
	var requests int64
	for _, usage := range summary.Usage {
		requests += usage.Requests
		if usage.Session == "" || usage.Model == "" || usage.Input == nil || usage.Output == nil || usage.CacheRead == nil {
			t.Errorf("%s usage row is missing fields: %+v", label, usage)
		}
	}
	if requests < 2 {
		t.Errorf("%s usage counted %d model requests for a tool turn", label, requests)
	}
}

func requireExactWireCapture(t *testing.T, label string, run *telemetryRun, keys []string) {
	t.Helper()
	if err := run.recorder.Close(); err != nil {
		t.Fatal(err)
	}
	run.wire.mu.Lock()
	defer run.wire.mu.Unlock()
	received := map[string]bool{}
	for _, body := range run.wire.requests {
		received[string(body)] = true
	}
	exchanges, promptSeen, responseSeen, keysSeen := 0, false, false, map[string]bool{}
	if err := wirecapture.Each(run.root, run.context.RunID, func(exchange wirecapture.Exchange, request, response []byte) error {
		exchanges++
		if !received[string(request)] {
			t.Errorf("%s captured a request the model never received, %d bytes", label, len(request))
		}
		if !exchange.Request.Complete || !exchange.Response.Complete {
			t.Errorf("%s exchange is incomplete: %+v %+v", label, exchange.Request, exchange.Response)
		}
		promptSeen = promptSeen || bytes.Contains(request, []byte(run.sentinels.Prompt))
		responseSeen = responseSeen || bytes.Contains(response, []byte(run.sentinels.Response))
		for _, key := range keys {
			keysSeen[key] = keysSeen[key] || bytes.Contains(request, []byte(`"`+key+`"`))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if exchanges != len(run.wire.requests) {
		t.Errorf("%s captured %d of %d model requests", label, exchanges, len(run.wire.requests))
	}
	if !promptSeen || !responseSeen {
		t.Errorf("%s capture missed content: prompt %v response %v", label, promptSeen, responseSeen)
	}
	for _, key := range keys {
		if !keysSeen[key] {
			t.Errorf("%s captured requests never held %q", label, key)
		}
	}
	dir, err := run.root.RunDir(run.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.Walk(filepath.Join(dir, evidence.WireDir), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(placeholderCredential)) {
			t.Errorf("%s holds the run placeholder", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	summary, err := wirecapture.Summarize(run.root, run.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s wire: %+v", label, summary)
}

func TestAgentRunsNormallyWithCaptureOffAndStoresNoBodies(t *testing.T) {
	cli := buildCLIImage(t, "claude", pinnedClaudeVersion)
	run := newCapturingRun(t, cli, claudeModel, 0, wirecapture.Settings{Enabled: false})
	run.execute(t, claudeRun(t, run))
	if err := run.recorder.Close(); err != nil {
		t.Fatal(err)
	}
	dir, err := run.root.RunDir(run.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, evidence.WireDir)); !os.IsNotExist(err) {
		t.Fatalf("capture wrote while off: %v", err)
	}
	requireNoInlineBodies(t, "capture off", run.stored(t, "capture off", "logs"))
}

func TestTelemetryExportedBeforeAContainerKillIsStored(t *testing.T) {
	for _, c := range agentCases {
		t.Run(c.agent, func(t *testing.T) {
			cli := buildCLIImage(t, c.agent, c.pinned)
			run := newTelemetryRun(t, cli, c.model, killHold)
			spec := c.run(t, run)
			run.executeAndKill(t, spec, killAfterExported)
			requireContent(t, spec.label, run.stored(t, spec.label+"-killed", "logs", "spans"), run.sentinels.byLabel(), "prompt", "tool input", "tool output")
		})
	}
}

func TestAgentKeepsWorkingWhenEvidenceStorageFailsAndTheLossIsRecorded(t *testing.T) {
	cli := buildCLIImage(t, "claude", pinnedClaudeVersion)
	run := newTelemetryRun(t, cli, claudeModel, 0)
	runDir, err := run.root.RunDir(run.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runDir, 0o500); err != nil {
		t.Fatal(err)
	}
	run.execute(t, claudeRun(t, run))
	if err := os.Chmod(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	run.finishStats()
	if err := run.recorder.Close(); err != nil {
		t.Fatal(err)
	}
	summary, err := telemetry.Summarize(run.root, run.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Receiver == nil || summary.Receiver.Failed == 0 || summary.Receiver.Stored != 0 {
		t.Fatalf("lost telemetry is not recorded: %+v", summary.Receiver)
	}
	wire, err := wirecapture.Summarize(run.root, run.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if wire.Failed == 0 || wire.Exchanges != 0 {
		t.Fatalf("lost captures are not recorded: %+v", wire)
	}
	if summary.TelemetryBytes != 0 {
		t.Fatalf("telemetry stored while storage was failing: %d bytes", summary.TelemetryBytes)
	}
}
