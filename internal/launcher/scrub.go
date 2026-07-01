package launcher

import (
	"strconv"
	"strings"
)

var scrubbedEnvVars = []string{
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"GH_ENTERPRISE_TOKEN",
	"GITHUB_ENTERPRISE_TOKEN",
	"GH_HOST",

	"SSH_AUTH_SOCK",
	"GIT_SSH",
	"GIT_SSH_COMMAND",
	"SSH_ASKPASS",

	"GIT_ASKPASS",

	"GIT_CONFIG_GLOBAL",
	"GIT_CONFIG_SYSTEM",

	"GIT_CONFIG_COUNT",

	"AI_AGENT_AUTH_SOCK",
	"AI_AGENT_SESSION_ID",
	"AI_AGENT_SESSION_BIND_FD",
	"AI_AGENT_SESSION_REPO",
	"AI_AGENT_RUN_ID",
	"AI_AGENT_TASK_REF",
	"AI_AGENT_REAL_GH",

	"AI_AGENT_LANGFUSE_PUBLIC_KEY",
	"AI_AGENT_LANGFUSE_SECRET_KEY",
	"LANGFUSE_PUBLIC_KEY",
	"LANGFUSE_SECRET_KEY",
	"AI_AGENT_OTLP_HEADERS",
	"OTEL_EXPORTER_OTLP_HEADERS",
	"OTEL_EXPORTER_OTLP_LOGS_HEADERS",
	"OTEL_EXPORTER_OTLP_TRACES_HEADERS",
	"AI_AGENT_OTLP_TRACES_ENDPOINT",
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
	"CLAUDE_CODE_ENABLE_TELEMETRY",
	"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA",
	"OTEL_LOG_USER_PROMPTS",
	"OTEL_LOG_TOOL_DETAILS",
	"OTEL_LOG_TOOL_CONTENT",
	"OTEL_LOG_RAW_API_BODIES",
	"AI_AGENT_LANGFUSE_HOST",
	"LANGFUSE_HOST",
	"AI_AGENT_OBSERVABILITY_RESOURCE",
	"AI_AGENT_CONTAINER",
}

var forcedEnvVars = map[string]string{
	"GIT_TERMINAL_PROMPT": "0",
}

func ScrubEnv(env []string, credentialHelperPath string, socketPath string, sessionID string, bindFD int, sessionRepo string, ghWrapperDir string, realGhPath string) []string {
	scrubSet := make(map[string]bool, len(scrubbedEnvVars))
	for _, v := range scrubbedEnvVars {
		scrubSet[v] = true
	}

	for _, e := range env {
		for _, prefix := range []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"} {
			if strings.HasPrefix(e, prefix) {
				if key, _, ok := strings.Cut(e, "="); ok {
					scrubSet[key] = true
				}
			}
		}
	}

	result := make([]string, 0, len(env))
	for _, e := range env {
		key, _, ok := strings.Cut(e, "=")
		if !ok || key == "" {
			continue
		}
		if scrubSet[key] {
			continue
		}
		result = append(result, e)
	}

	for k, v := range forcedEnvVars {
		result = append(result, k+"="+v)
	}

	result = append(result, "AI_AGENT_AUTH_SOCK="+socketPath)
	result = append(result, "AI_AGENT_SESSION_ID="+sessionID)
	result = append(result, "AI_AGENT_SESSION_BIND_FD="+strconv.Itoa(bindFD))
	result = append(result, "AI_AGENT_SESSION_REPO="+sessionRepo)
	if realGhPath != "" {
		result = append(result, "AI_AGENT_REAL_GH="+realGhPath)
	}

	if ghWrapperDir != "" {
		result = prependPath(result, ghWrapperDir)
	}

	repoURL := "https://github.com/" + sessionRepo
	result = append(result,
		"GIT_CONFIG_COUNT=7",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=credential.helper",
		"GIT_CONFIG_VALUE_1="+credentialHelperPath,
		"GIT_CONFIG_KEY_2=credential.https://github.com.useHttpPath",
		"GIT_CONFIG_VALUE_2=true",
		"GIT_CONFIG_KEY_3=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_3=",
		"GIT_CONFIG_KEY_4=http."+repoURL+".extraheader",
		"GIT_CONFIG_VALUE_4=",
		"GIT_CONFIG_KEY_5=http."+repoURL+".git.extraheader",
		"GIT_CONFIG_VALUE_5=",
		"GIT_CONFIG_KEY_6=http.extraheader",
		"GIT_CONFIG_VALUE_6=",
	)

	return result
}

func prependPath(env []string, dir string) []string {
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + dir + ":" + e[5:]
			return env
		}
	}
	return append(env, "PATH="+dir)
}
