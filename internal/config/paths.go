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

// RuntimeDir returns the runtime directory for ai-agent.
// Priority: $XDG_RUNTIME_DIR/ai-agent > /run/user/<uid>/ai-agent
func RuntimeDir() string {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "ai-agent")
	}
	return fmt.Sprintf("/run/user/%d/ai-agent", os.Getuid())
}

// DefaultPolicyPath returns the default path for the policy file.
func DefaultPolicyPath() string {
	return filepath.Join(ConfigDir(), "policy.json")
}

// DefaultIdentitiesPath returns the default path for the identities file.
func DefaultIdentitiesPath() string {
	return filepath.Join(ConfigDir(), "identities.json")
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
