package github

import (
	"encoding/json"
	"fmt"
)

// Config is the parsed per-agent GitHub policy section.
type Config struct {
	InstallationID     int64
	AppID              string
	DefaultPermissions map[string]string
}

type rawConfig struct {
	InstallationID     int64             `json:"installation_id"`
	AppID              string            `json:"app_id,omitempty"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}

func parseConfig(agent string, section json.RawMessage, resolveAppID func(string) string) (Config, error) {
	if len(section) == 0 || string(section) == "null" {
		return Config{}, fmt.Errorf("agent %q: missing providers.github section", agent)
	}

	var raw rawConfig
	if err := json.Unmarshal(section, &raw); err != nil {
		return Config{}, fmt.Errorf("agent %q: providers.github: %w", agent, err)
	}

	if raw.InstallationID <= 0 {
		return Config{}, fmt.Errorf("agent %q: providers.github.installation_id must be > 0", agent)
	}
	if len(raw.DefaultPermissions) == 0 {
		return Config{}, fmt.Errorf("agent %q: providers.github.default_permissions must not be empty", agent)
	}
	for key, val := range raw.DefaultPermissions {
		if !isKnownPermissionKey(key) {
			return Config{}, fmt.Errorf("agent %q: providers.github.default_permissions[%q]: unknown permission key", agent, key)
		}
		if levelOf(val) == levelUnknown {
			return Config{}, fmt.Errorf("agent %q: providers.github.default_permissions[%q]: invalid level %q", agent, key, val)
		}
	}

	appID := raw.AppID
	if appID == "" {
		appID = resolveAppID(agent)
	}
	if appID == "" {
		return Config{}, fmt.Errorf("agent %q: providers.github.app_id not set and no identity match", agent)
	}

	return Config{
		InstallationID:     raw.InstallationID,
		AppID:              appID,
		DefaultPermissions: raw.DefaultPermissions,
	}, nil
}
