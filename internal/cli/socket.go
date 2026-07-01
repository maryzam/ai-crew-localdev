package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func resolveBrokerSocketPath(override string) (string, error) {
	if override != "" {
		return validateBrokerSocketPath(override, "broker socket path")
	}
	if socketPath, ok := os.LookupEnv("AI_AGENT_AUTH_SOCK"); ok {
		trimmed := strings.TrimSpace(socketPath)
		if trimmed == "" {
			return paths.DefaultSocketPath(), nil
		}
		return validateBrokerSocketPath(trimmed, "AI_AGENT_AUTH_SOCK")
	}
	return paths.DefaultSocketPath(), nil
}

func resolveSessionBrokerSocketPath(override, stored string) (string, error) {
	if override != "" {
		return resolveBrokerSocketPath(override)
	}
	if stored != "" {
		return validateBrokerSocketPath(stored, "session file socket path")
	}
	return resolveBrokerSocketPath("")
}

func validateBrokerSocketPath(path, source string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("invalid %s: must not be empty", source)
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("invalid %s: must be an absolute path", source)
	}
	return filepath.Clean(trimmed), nil
}
