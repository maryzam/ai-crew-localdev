package policy

import "encoding/json"

type PolicyFile struct {
	SchemaVersion      string                 `json:"schema_version"`
	DefaultSessionTTL  string                 `json:"default_session_ttl"`
	DefaultIdleTimeout string                 `json:"default_idle_timeout"`
	Agents             map[string]AgentPolicy `json:"agents"`
}

type AgentPolicy struct {
	Resources []string                   `json:"resources"`
	Providers map[string]json.RawMessage `json:"providers,omitempty"`
}
