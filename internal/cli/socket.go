package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func resolveBrokerSocketPath(override string) (string, error) {
	if override != "" {
		return validateBrokerSocketPath(override, "broker socket path")
	}
	socketPath, source := paths.BrokerClientSocket()
	if source == "" {
		return socketPath, nil
	}
	return validateBrokerSocketPath(socketPath, source)
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
