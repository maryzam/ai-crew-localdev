package policy

// PolicyFile is the top-level policy configuration. Agents declare their
// granted resources as URI strings ("github:repo:owner/name", ...) and
// provide flat per-provider configuration sections (`github:`, future
// `aws:`). See docs/decisions/0001-credential-generic-broker-api.md.
type PolicyFile struct {
	SchemaVersion      string                 `json:"schema_version"`
	DefaultSessionTTL  string                 `json:"default_session_ttl"`
	DefaultIdleTimeout string                 `json:"default_idle_timeout"`
	Agents             map[string]AgentPolicy `json:"agents"`
}

// AgentPolicy is the per-agent policy. Resources is the credential-generic
// set of resource URIs the agent may request; per-provider configuration
// lives in flat optional sections (currently only GitHub).
type AgentPolicy struct {
	Resources []string           `json:"resources"`
	GitHub    *GitHubAgentConfig `json:"github,omitempty"`
}

// GitHubAgentConfig is the per-agent GitHub configuration. AppID is
// optional; if omitted the broker's signer falls back to the identities
// file's default AppID for the agent. DefaultPermissions follows the
// standard GitHub permissions schema.
type GitHubAgentConfig struct {
	InstallationID     int64             `json:"installation_id"`
	AppID              string            `json:"app_id,omitempty"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}
