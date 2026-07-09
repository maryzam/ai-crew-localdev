package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ConfigDir() string {
	if dir := os.Getenv(EnvConfigDir); dir != "" {
		return dir
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ai-agent")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ai-agent")
}

func RuntimeBaseDir() string {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return xdg
	}
	return fmt.Sprintf("/run/user/%d", os.Getuid())
}

func RuntimeDir() string {
	return filepath.Join(RuntimeBaseDir(), "ai-agent")
}

func DefaultPolicyPath() string {
	return filepath.Join(ConfigDir(), "policy.json")
}

func DefaultIdentitiesPath() string {
	return filepath.Join(ConfigDir(), "identities.json")
}

func DefaultSocketPath() string {
	return filepath.Join(RuntimeDir(), "broker.sock")
}

func DefaultAuditLogPath() string {
	return filepath.Join(ConfigDir(), "audit.log")
}

func DefaultRunTelemetryPath() string {
	return filepath.Join(ConfigDir(), "run-telemetry.jsonl")
}

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
