package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvAuthSock              = "AI_AGENT_AUTH_SOCK"
	EnvBrokerSocket          = "AI_AGENT_BROKER_SOCKET"
	EnvSessionID             = "AI_AGENT_SESSION_ID"
	EnvSessionBindFD         = "AI_AGENT_SESSION_BIND_FD"
	EnvSessionRepo           = "AI_AGENT_SESSION_REPO"
	EnvRunID                 = "AI_AGENT_RUN_ID"
	EnvTaskRef               = "AI_AGENT_TASK_REF"
	EnvContainer             = "AI_AGENT_CONTAINER"
	EnvRealGh                = "AI_AGENT_REAL_GH"
	EnvModel                 = "AI_AGENT_MODEL"
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
	EnvLangfusePublicKey     = "AI_AGENT_LANGFUSE_PUBLIC_KEY"
	EnvLangfuseSecretKey     = "AI_AGENT_LANGFUSE_SECRET_KEY"
	EnvLangfuseHost          = "AI_AGENT_LANGFUSE_HOST"
	EnvLangfuseOTLPEndpoint  = "AI_AGENT_LANGFUSE_OTLP_ENDPOINT"
	EnvOTLPHeaders           = "AI_AGENT_OTLP_HEADERS"
	EnvOTLPTracesEndpoint    = "AI_AGENT_OTLP_TRACES_ENDPOINT"
)

func ValidateSocketPath(path, source string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("invalid %s: must not be empty", source)
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("invalid %s: must be an absolute path", source)
	}
	return filepath.Clean(trimmed), nil
}

func BrokerListenSocket() (path string, fromEnv bool, err error) {
	if raw := os.Getenv(EnvBrokerSocket); strings.TrimSpace(raw) != "" {
		path, err = ValidateSocketPath(raw, EnvBrokerSocket)
		return path, true, err
	}
	return defaultSocketPath(), false, nil
}

func BrokerListenSocketPath() (string, error) {
	path, _, err := BrokerListenSocket()
	return path, err
}

func BrokerClientSocket() (path string, source string, err error) {
	if raw, ok := os.LookupEnv(EnvAuthSock); ok && strings.TrimSpace(raw) != "" {
		path, err = ValidateSocketPath(raw, EnvAuthSock)
		return path, EnvAuthSock, err
	}
	if raw := os.Getenv(EnvBrokerSocket); strings.TrimSpace(raw) != "" {
		path, err = ValidateSocketPath(raw, EnvBrokerSocket)
		return path, EnvBrokerSocket, err
	}
	return defaultSocketPath(), "", nil
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
