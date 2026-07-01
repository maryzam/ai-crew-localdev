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

func TestObservabilityEndpointUsesLoopbackOutsideContainer(t *testing.T) {
	t.Setenv("AI_AGENT_CONTAINER", "")
	got := observabilityEndpoint("http://host.containers.internal:3000/api/public/otel")
	if got != "http://127.0.0.1:3000/api/public/otel" {
		t.Fatalf("endpoint = %q", got)
	}
}
