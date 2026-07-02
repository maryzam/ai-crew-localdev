package launcher

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
)

type nativeTelemetryRelay interface {
	Endpoint() string
	Authorization() string
}

type telemetryPublisher interface {
	PublishTelemetry(api.PublishTelemetryRequest) (*api.PublishTelemetryResponse, error)
}

type brokerTelemetryExporter struct {
	client     telemetryPublisher
	sessionID  string
	bindSecret []byte
	resource   string
}

func (e *brokerTelemetryExporter) Export(payload []byte) error {
	response, err := e.client.PublishTelemetry(api.PublishTelemetryRequest{
		SessionID:  e.sessionID,
		BindSecret: e.bindSecret,
		Resource:   e.resource,
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

func acceptedBytes(response *api.PublishTelemetryResponse) int {
	if response == nil {
		return 0
	}
	return response.AcceptedBytes
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
