package contract

import (
	"github.com/maryzam/ai-crew-localdev/internal/interception"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func InterceptionProfile() interception.Profile {
	return interception.Profile{
		Provider: "langfuse",
		ScrubEnv: []string{
			paths.EnvLangfusePublicKey,
			paths.EnvLangfuseSecretKey,
			"LANGFUSE_PUBLIC_KEY",
			"LANGFUSE_SECRET_KEY",
			paths.EnvLangfuseHost,
			"LANGFUSE_HOST",
			paths.EnvOTLPHeaders,
			paths.EnvOTLPTracesEndpoint,
			paths.EnvObservabilityResource,
			"OTEL_EXPORTER_OTLP_HEADERS",
			"OTEL_EXPORTER_OTLP_LOGS_HEADERS",
			"OTEL_EXPORTER_OTLP_TRACES_HEADERS",
			"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
			"OTEL_EXPORTER_OTLP_ENDPOINT",
			"OTEL_EXPORTER_OTLP_PROTOCOL",
			"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL",
			"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
			"OTEL_LOGS_EXPORTER",
			"OTEL_TRACES_EXPORTER",
			"OTEL_METRICS_EXPORTER",
			"OTEL_RESOURCE_ATTRIBUTES",
			"OTEL_LOGS_EXPORT_INTERVAL",
			"OTEL_TRACES_EXPORT_INTERVAL",
			"OTEL_METRICS_INCLUDE_ACCOUNT_UUID",
			"OTEL_LOG_USER_PROMPTS",
			"OTEL_LOG_TOOL_DETAILS",
			"OTEL_LOG_TOOL_CONTENT",
			"OTEL_LOG_RAW_API_BODIES",
			"CLAUDE_CODE_ENABLE_TELEMETRY",
			"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA",
		},
	}
}
