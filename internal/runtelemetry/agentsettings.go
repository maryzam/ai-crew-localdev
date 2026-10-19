package runtelemetry

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

const (
	containerOTLPHost          = "127.0.0.1"
	containerOTLPPort          = "4318"
	ContainerEndpoint          = "http://" + containerOTLPHost + ":" + containerOTLPPort
	ContainerRunSocket         = "/run/ai-crew/run.sock"
	ClaudeManagedSettingsPath  = "/etc/claude-code/managed-settings.json"
	CodexManagedConfigPath     = "/etc/codex/managed_config.toml"
	OTLPForwarderPath          = "/usr/local/bin/ai-crew-otlp-forward"
	ContainerAgentUser         = "node"
	ContainerAgentUID          = 1000
	CodexToolResultMaxBytes    = 1 << 20
	ExportIntervalMillis       = 1000
	MetricExportIntervalMillis = 10000
)

type ImageFile struct {
	Content []byte
	Mode    os.FileMode
}

func ImageFiles() map[string]ImageFile {
	return map[string]ImageFile{
		ClaudeManagedSettingsPath: {ClaudeManagedSettings(), 0o644},
		CodexManagedConfigPath:    {CodexManagedConfig(), 0o644},
		OTLPForwarderPath:         {otlpForwarderScript(), 0o755},
	}
}

func ImageEnv() []string {
	return []string{
		"OTEL_BLRP_SCHEDULE_DELAY=" + strconv.Itoa(ExportIntervalMillis),
		"OTEL_BSP_SCHEDULE_DELAY=" + strconv.Itoa(ExportIntervalMillis),
		"OTEL_METRIC_EXPORT_INTERVAL=" + strconv.Itoa(MetricExportIntervalMillis),
	}
}

func otlpForwarderScript() []byte {
	listen := "TCP-LISTEN:" + containerOTLPPort + ",bind=" + containerOTLPHost + ",reuseaddr,fork"
	probe := "TCP:" + containerOTLPHost + ":" + containerOTLPPort
	return []byte(strings.Join([]string{
		"#!/bin/sh",
		"set -eu",
		`[ "$(id -u)" = 0 ] || { echo "the telemetry forwarder must start as root so the agent cannot stop it" >&2; exit 1; }`,
		"socat " + listen + " UNIX-CONNECT:" + ContainerRunSocket + " &",
		"tries=0",
		"until socat -u OPEN:/dev/null " + probe + " 2>/dev/null; do",
		"  tries=$((tries + 1))",
		`  [ "$tries" -lt 50 ] || { echo "telemetry forwarder did not start" >&2; exit 1; }`,
		"  sleep 0.1",
		"done",
		"exec setpriv --reuid=" + ContainerAgentUser + " --regid=" + ContainerAgentUser + ` --init-groups -- "$@"`,
		"",
	}, "\n"))
}

func agentEndpoint(agent Agent) string {
	return ContainerEndpoint + routePrefix + string(agent)
}

func ClaudeManagedSettings() []byte {
	env := map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY":        "1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
		"OTEL_LOGS_EXPORTER":                  "otlp",
		"OTEL_METRICS_EXPORTER":               "otlp",
		"OTEL_TRACES_EXPORTER":                "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL":         "http/json",
		"OTEL_EXPORTER_OTLP_ENDPOINT":         agentEndpoint(Claude),
		"OTEL_LOGS_EXPORT_INTERVAL":           strconv.Itoa(ExportIntervalMillis),
		"OTEL_TRACES_EXPORT_INTERVAL":         strconv.Itoa(ExportIntervalMillis),
		"OTEL_METRIC_EXPORT_INTERVAL":         strconv.Itoa(MetricExportIntervalMillis),
		"OTEL_LOG_USER_PROMPTS":               "1",
		"OTEL_LOG_ASSISTANT_RESPONSES":        "1",
		"OTEL_LOG_TOOL_DETAILS":               "1",
		"OTEL_LOG_TOOL_CONTENT":               "1",
		"OTEL_LOG_RAW_API_BODIES":             "1",
	}
	data, _ := json.MarshalIndent(map[string]any{"env": env}, "", "  ")
	return append(data, '\n')
}

func CodexManagedConfig() []byte {
	exporter := func(signal string) string {
		return `{ otlp-http = { endpoint = "` + agentEndpoint(Codex) + `/v1/` + signal + `", protocol = "json" } }`
	}
	return []byte(strings.Join([]string{
		"[otel]",
		"log_user_prompt = true",
		"log_agent_responses = true",
		"log_guardian_assessments = true",
		"exporter = " + exporter("logs"),
		"trace_exporter = " + exporter("traces"),
		"metrics_exporter = " + exporter("metrics"),
		"",
		"[otel.tool_result]",
		"max_bytes = " + strconv.Itoa(CodexToolResultMaxBytes),
		"",
	}, "\n"))
}
