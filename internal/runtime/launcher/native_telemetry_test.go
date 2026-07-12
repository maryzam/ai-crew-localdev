package launcher

import (
	"strings"
	"testing"
)

type stubNativeRelay struct{}

func (stubNativeRelay) Endpoint() string      { return "http://127.0.0.1:4318" }
func (stubNativeRelay) Authorization() string { return "Bearer publish-token" }

func TestNativeTelemetryEnvConfiguresClaudeWithoutBackendCredential(t *testing.T) {
	env := nativeTelemetryEnv([]string{"PATH=/usr/bin"}, []string{"claude"}, stubNativeRelay{}, "run_123")
	joined := strings.Join(env, "\n")
	for _, expected := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"OTEL_LOGS_EXPORTER=otlp",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://127.0.0.1:4318/v1/logs",
		"OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%20publish-token",
		"OTEL_LOG_USER_PROMPTS=0",
		"OTEL_LOG_TOOL_CONTENT=0",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("environment missing %q", expected)
		}
	}
	for _, forbidden := range []string{"LANGFUSE_PUBLIC_KEY", "LANGFUSE_SECRET_KEY"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("environment leaked %q", forbidden)
		}
	}
}

func TestNativeTelemetryCommandConfiguresCodexWithPublishToken(t *testing.T) {
	command := nativeTelemetryCommand([]string{"codex", "exec", "fix tests"}, stubNativeRelay{})
	joined := strings.Join(command, " ")
	for _, expected := range []string{
		"otel.log_user_prompt=false",
		"http://127.0.0.1:4318/v1/logs",
		"http://127.0.0.1:4318/v1/traces",
		`Authorization="Bearer publish-token"`,
		"exec fix tests",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("command missing %q: %q", expected, command)
		}
	}
}

func TestNativeTelemetryEnvAddsRunIDForGenericExporterRelay(t *testing.T) {
	env := nativeTelemetryEnv([]string{"PATH=/usr/bin"}, []string{"custom-agent"}, stubNativeRelay{}, "run_123")
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "OTEL_RESOURCE_ATTRIBUTES=ai_agent.run.id=run_123") {
		t.Fatalf("generic telemetry relay env missing run id: %s", joined)
	}
	if strings.Contains(joined, "CLAUDE_CODE_ENABLE_TELEMETRY") {
		t.Fatalf("generic command received claude-specific telemetry env: %s", joined)
	}
}

type scopedRelay struct{}

const durableLangfuseSecret = "sk-lf-DURABLE-SECRET-must-stay-broker-side"

func (scopedRelay) Endpoint() string      { return "http://127.0.0.1:4318" }
func (scopedRelay) Authorization() string { return "Bearer relay-scoped-token" }

func TestWorkspaceReceivesOnlyRelayTokenNotDurableSecret(t *testing.T) {
	relay := scopedRelay{}

	claudeEnv := strings.Join(nativeTelemetryEnv([]string{"PATH=/usr/bin"}, []string{"claude"}, relay, "run_123"), "\n")
	codexCommand := strings.Join(nativeTelemetryCommand([]string{"codex", "exec", "fix"}, relay), " ")

	for surface, value := range map[string]string{"claude env": claudeEnv, "codex command": codexCommand} {
		if strings.Contains(value, durableLangfuseSecret) {
			t.Errorf("%s leaked the durable langfuse secret", surface)
		}
		if !strings.Contains(value, "relay-scoped-token") {
			t.Errorf("%s missing the scoped relay token", surface)
		}
		if !strings.Contains(value, "127.0.0.1:4318") {
			t.Errorf("%s not pointed at the loopback relay", surface)
		}
	}
}

func TestNativeTelemetryCoversSupportedAuthenticationModes(t *testing.T) {
	tests := []struct {
		name       string
		env        []string
		command    []string
		authMarker string
	}{
		{name: "claude subscription OAuth", env: []string{"PATH=/usr/bin"}, command: []string{"claude"}},
		{name: "claude API key", env: []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=claude-personal-key"}, command: []string{"claude"}, authMarker: "ANTHROPIC_API_KEY=claude-personal-key"},
		{name: "codex ChatGPT sign-in", env: []string{"PATH=/usr/bin"}, command: []string{"codex", "exec", "task"}},
		{name: "codex API key", env: []string{"PATH=/usr/bin", "OPENAI_API_KEY=codex-personal-key"}, command: []string{"codex", "exec", "task"}, authMarker: "OPENAI_API_KEY=codex-personal-key"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !nativeTelemetrySupported(test.command) {
				t.Fatalf("command is not covered: %q", test.command)
			}
			env := strings.Join(nativeTelemetryEnv(test.env, test.command, stubNativeRelay{}, "run_123"), "\n")
			command := strings.Join(nativeTelemetryCommand(test.command, stubNativeRelay{}), " ")
			combined := env + "\n" + command
			if !strings.Contains(combined, "http://127.0.0.1:4318/v1/logs") || !strings.Contains(combined, "publish-token") {
				t.Fatalf("native telemetry missing for %s: %s", test.name, combined)
			}
			if test.authMarker != "" && !strings.Contains(env, test.authMarker) {
				t.Fatalf("personal authentication was not preserved for %s", test.name)
			}
		})
	}
}
