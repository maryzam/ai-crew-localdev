package launcher

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type nativeTelemetryRelay interface {
	Endpoint() string
	Authorization() string
}

func observabilityEndpoint(endpoint string) string {
	if os.Getenv("AI_AGENT_CONTAINER") == "1" {
		return endpoint
	}
	return strings.Replace(endpoint, "host.containers.internal", "127.0.0.1", 1)
}

func nativeTelemetryEnv(env, command []string, relay nativeTelemetryRelay, runID string) []string {
	if len(command) == 0 {
		return env
	}
	env = append(env, "OTEL_RESOURCE_ATTRIBUTES=ai_agent.run.id="+runID)
	if strings.TrimSuffix(filepath.Base(command[0]), ".exe") != "claude" {
		return env
	}
	encodedAuth := strings.ReplaceAll(url.QueryEscape(relay.Authorization()), "+", "%20")
	return append(env,
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"OTEL_LOGS_EXPORTER=otlp",
		"OTEL_TRACES_EXPORTER=otlp",
		"OTEL_METRICS_EXPORTER=none",
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/json",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="+relay.Endpoint()+"/v1/logs",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="+relay.Endpoint()+"/v1/traces",
		"OTEL_EXPORTER_OTLP_HEADERS=Authorization="+encodedAuth,
		"OTEL_LOGS_EXPORT_INTERVAL=1000",
		"OTEL_TRACES_EXPORT_INTERVAL=1000",
		"OTEL_METRICS_INCLUDE_ACCOUNT_UUID=false",
		"OTEL_LOG_USER_PROMPTS=0",
		"OTEL_LOG_TOOL_DETAILS=0",
		"OTEL_LOG_TOOL_CONTENT=0",
	)
}

func nativeTelemetryCommand(command []string, relay nativeTelemetryRelay) []string {
	if len(command) == 0 || strings.TrimSuffix(filepath.Base(command[0]), ".exe") != "codex" {
		return command
	}
	header := "headers={Authorization=" + strconv.Quote(relay.Authorization()) + "}"
	logs := "otel.exporter={otlp-http={endpoint=" + strconv.Quote(relay.Endpoint()+"/v1/logs") + ",protocol=\"json\"," + header + "}}"
	traces := "otel.trace_exporter={otlp-http={endpoint=" + strconv.Quote(relay.Endpoint()+"/v1/traces") + ",protocol=\"json\"," + header + "}}"
	result := make([]string, 0, len(command)+6)
	result = append(result, command[0], "-c", "otel.log_user_prompt=false", "-c", logs, "-c", traces)
	return append(result, command[1:]...)
}
