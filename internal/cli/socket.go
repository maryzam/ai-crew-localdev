package cli

import (
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func resolveBrokerSocketPath(override string) (string, error) {
	if override != "" {
		return validateBrokerSocketPath(override, "broker socket path")
	}
	socketPath, _, err := paths.BrokerClientSocket()
	return socketPath, err
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
	return paths.ValidateSocketPath(path, source)
}
