package cli

import (
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/config"
)

func resolveBrokerSocketPath(override string) string {
	if override != "" {
		return override
	}
	if socketPath := os.Getenv("AI_AGENT_AUTH_SOCK"); socketPath != "" {
		return socketPath
	}
	return config.DefaultSocketPath()
}
