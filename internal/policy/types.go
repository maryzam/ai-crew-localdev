package policy

import "encoding/json"

// PolicyFile is the top-level policy configuration.
type PolicyFile struct {
	SchemaVersion      string                 `json:"schema_version"`
	DefaultSessionTTL  string                 `json:"default_session_ttl"`
	DefaultIdleTimeout string                 `json:"default_idle_timeout"`
	Agents             map[string]AgentPolicy `json:"agents"`
}

// AgentPolicy declares the resources an agent may request and the per-provider
// configuration sections the broker hands to each CredentialProvider.
type AgentPolicy struct {
	Resources []string                   `json:"resources"`
	Providers map[string]json.RawMessage `json:"providers,omitempty"`
}
