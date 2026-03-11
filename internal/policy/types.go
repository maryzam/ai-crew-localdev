package policy

// PolicyFile represents the top-level policy configuration file.
type PolicyFile struct {
	SchemaVersion      string                 `json:"schema_version"`
	DefaultSessionTTL  string                 `json:"default_session_ttl"`
	DefaultIdleTimeout string                 `json:"default_idle_timeout"`
	Agents             map[string]AgentPolicy `json:"agents"`
}

// AgentPolicy represents policy configuration for a single AI agent.
type AgentPolicy struct {
	AllowedRepos       []string          `json:"allowed_repos"`
	InstallationID     *int64            `json:"installation_id,omitempty"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}
