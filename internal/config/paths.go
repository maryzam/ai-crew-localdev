package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigDir returns the configuration directory for ai-agent.
// Priority: $AI_AGENT_CONFIG_DIR > $XDG_CONFIG_HOME/ai-agent > ~/.config/ai-agent
func ConfigDir() string {
	if dir := os.Getenv("AI_AGENT_CONFIG_DIR"); dir != "" {
		return dir
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ai-agent")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ai-agent")
}

// RuntimeBaseDir returns the per-user runtime directory root.
// Priority: $XDG_RUNTIME_DIR > /run/user/<uid>
func RuntimeBaseDir() string {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return xdg
	}
	return fmt.Sprintf("/run/user/%d", os.Getuid())
}

// RuntimeDir returns the runtime directory for ai-agent.
// Priority: $XDG_RUNTIME_DIR/ai-agent > /run/user/<uid>/ai-agent
func RuntimeDir() string {
	return filepath.Join(RuntimeBaseDir(), "ai-agent")
}

// DefaultPolicyPath returns the default path for the policy file.
func DefaultPolicyPath() string {
	return filepath.Join(ConfigDir(), "policy.json")
}

// DefaultIdentitiesPath returns the default path for the identities file.
func DefaultIdentitiesPath() string {
	return filepath.Join(ConfigDir(), "identities.json")
}

// DefaultSocketPath returns the default broker socket path.
func DefaultSocketPath() string {
	return filepath.Join(RuntimeDir(), "broker.sock")
}

// DefaultAuditLogPath returns the default audit log path.
func DefaultAuditLogPath() string {
	return filepath.Join(ConfigDir(), "audit.log")
}

func DefaultRunTelemetryPath() string {
	return filepath.Join(ConfigDir(), "run-telemetry.jsonl")
}

// ExpandHome expands a leading ~ in a path to the user's home directory.
func ExpandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
