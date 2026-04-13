package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/config"
)

func resolveBrokerSocketPath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if socketPath, ok := os.LookupEnv("AI_AGENT_AUTH_SOCK"); ok && socketPath != "" {
		if strings.TrimSpace(socketPath) == "" {
			return "", errInvalidAuthSocketEnv("must not be whitespace-only")
		}
		if !filepath.IsAbs(socketPath) {
			return "", errInvalidAuthSocketEnv("must be an absolute path")
		}
		return socketPath, nil
	}
	return config.DefaultSocketPath(), nil
}

func errInvalidAuthSocketEnv(reason string) error {
	return &brokerSocketEnvError{Reason: reason}
}

type brokerSocketEnvError struct {
	Reason string
}

func (e *brokerSocketEnvError) Error() string {
	return "invalid AI_AGENT_AUTH_SOCK: " + e.Reason
}
