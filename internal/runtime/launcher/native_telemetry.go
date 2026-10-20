package launcher

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	agentcaps "github.com/maryzam/ai-crew-localdev/internal/agents/capabilities"
	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
)

type nativeTelemetryRelay interface {
	Endpoint() string
	Authorization() string
}

type telemetryPublisher interface {
	RecordTelemetry(api.RecordTelemetryRequest) (*api.RecordTelemetryResponse, error)
}

func nativeTelemetrySupported(command []string) bool {
	_, ok := agentcaps.NativeTelemetryForCommand(command)
	return ok
}

type brokerTelemetryExporter struct {
	client     telemetryPublisher
	sessionID  string
	bindSecret []byte
}

func (e *brokerTelemetryExporter) Export(signal string, payload []byte) error {
	response, err := e.client.RecordTelemetry(api.RecordTelemetryRequest{
		SessionID:  e.sessionID,
		BindSecret: e.bindSecret,
		Signal:     signal,
		Payload:    payload,
	})
	if err != nil {
		return err
	}
	if response == nil || response.AcceptedBytes != len(payload) {
		return fmt.Errorf("broker accepted %d of %d telemetry bytes", acceptedBytes(response), len(payload))
	}
	return nil
}

func acceptedBytes(response *api.RecordTelemetryResponse) int {
	if response == nil {
		return 0
	}
	return response.AcceptedBytes
}

func nativeTelemetryEnv(env, command []string, relay nativeTelemetryRelay, runID string) []string {
	env = append(env, "OTEL_RESOURCE_ATTRIBUTES=ai_agent.run.id="+runID)
	telemetry, ok := agentcaps.NativeTelemetryForCommand(command)
	if !ok {
		return env
	}
	if telemetry.Surface != agentcaps.NativeTelemetryEnv {
		return env
	}
	encodedAuth := strings.ReplaceAll(url.QueryEscape(relay.Authorization()), "+", "%20")
	return append(env,
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"OTEL_LOGS_EXPORTER=otlp",
		"OTEL_TRACES_EXPORTER=otlp",
		"OTEL_METRICS_EXPORTER=otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/json",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="+relay.Endpoint()+"/v1/logs",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="+relay.Endpoint()+"/v1/traces",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT="+relay.Endpoint()+"/v1/metrics",
		"OTEL_EXPORTER_OTLP_HEADERS=Authorization="+encodedAuth,
		"OTEL_LOGS_EXPORT_INTERVAL=1000",
		"OTEL_TRACES_EXPORT_INTERVAL=1000",
		"OTEL_METRIC_EXPORT_INTERVAL=10000",
		"OTEL_METRICS_INCLUDE_ACCOUNT_UUID=false",
		"OTEL_LOG_USER_PROMPTS=1",
		"OTEL_LOG_ASSISTANT_RESPONSES=1",
		"OTEL_LOG_TOOL_DETAILS=1",
		"OTEL_LOG_TOOL_CONTENT=1",
		"OTEL_LOG_RAW_API_BODIES=1",
	)
}

func nativeTelemetryCommand(command []string, relay nativeTelemetryRelay) []string {
	telemetry, ok := agentcaps.NativeTelemetryForCommand(command)
	if !ok || telemetry.Surface != agentcaps.NativeTelemetryCommand {
		return command
	}
	header := "headers={Authorization=" + strconv.Quote(relay.Authorization()) + "}"
	logs := "otel.exporter={otlp-http={endpoint=" + strconv.Quote(relay.Endpoint()+"/v1/logs") + ",protocol=\"json\"," + header + "}}"
	traces := "otel.trace_exporter={otlp-http={endpoint=" + strconv.Quote(relay.Endpoint()+"/v1/traces") + ",protocol=\"json\"," + header + "}}"
	metrics := "otel.metrics_exporter={otlp-http={endpoint=" + strconv.Quote(relay.Endpoint()+"/v1/metrics") + ",protocol=\"json\"," + header + "}}"
	result := make([]string, 0, len(command)+16)
	result = append(result, command[0], "-c", "otel.log_user_prompt=true", "-c", "otel.log_agent_responses=true", "-c", "otel.log_guardian_assessments=true", "-c", "otel.tool_result.max_bytes=1048576", "-c", logs, "-c", traces, "-c", metrics)
	return append(result, command[1:]...)
}
