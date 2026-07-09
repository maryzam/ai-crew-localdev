package paths

import (
	"os"
	"strings"
)

const (
	EnvAuthSock              = "AI_AGENT_AUTH_SOCK"
	EnvBrokerSocket          = "AI_AGENT_BROKER_SOCKET"
	EnvSessionID             = "AI_AGENT_SESSION_ID"
	EnvSessionBindFD         = "AI_AGENT_SESSION_BIND_FD"
	EnvSessionRepo           = "AI_AGENT_SESSION_REPO"
	EnvRealGh                = "AI_AGENT_REAL_GH"
	EnvWorkspace             = "AI_AGENT_WORKSPACE"
	EnvObservabilityResource = "AI_AGENT_OBSERVABILITY_RESOURCE"
	EnvPolicyPath            = "AI_AGENT_POLICY_PATH"
	EnvAuditLog              = "AI_AGENT_AUDIT_LOG"
	EnvSessionTTL            = "AI_AGENT_SESSION_TTL"
	EnvIdleTimeout           = "AI_AGENT_IDLE_TIMEOUT"
	EnvGitHubBaseURL         = "AI_AGENT_GITHUB_BASE_URL"
	EnvTelemetry             = "AI_AGENT_TELEMETRY"
	EnvRunTelemetryLog       = "AI_AGENT_RUN_TELEMETRY_LOG"
	EnvConfigDir             = "AI_AGENT_CONFIG_DIR"
)

func BrokerListenSocketPath() string {
	if path := strings.TrimSpace(os.Getenv(EnvBrokerSocket)); path != "" {
		return path
	}
	return DefaultSocketPath()
}

func BrokerClientSocket() (path string, source string) {
	if value, ok := os.LookupEnv(EnvAuthSock); ok {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed, EnvAuthSock
		}
	}
	if trimmed := strings.TrimSpace(os.Getenv(EnvBrokerSocket)); trimmed != "" {
		return trimmed, EnvBrokerSocket
	}
	return DefaultSocketPath(), ""
}

func PolicyPath() string {
	if value := os.Getenv(EnvPolicyPath); value != "" {
		return ExpandHome(value)
	}
	return DefaultPolicyPath()
}

func AuditLogPath() string {
	if value := strings.TrimSpace(os.Getenv(EnvAuditLog)); value != "" {
		return ExpandHome(value)
	}
	return DefaultAuditLogPath()
}

func RunTelemetryLogPath() string {
	if value := strings.TrimSpace(os.Getenv(EnvRunTelemetryLog)); value != "" {
		return ExpandHome(value)
	}
	return DefaultRunTelemetryPath()
}

func TelemetryDisabled() bool {
	return os.Getenv(EnvTelemetry) == "disabled"
}
