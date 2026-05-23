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

// PolicyFileV2 is the credential-generic v2 policy schema. Agents declare
// their granted resources as URI strings ("github:repo:owner/name", ...)
// and provide flat per-provider configuration sections (`github:`,
// future `aws:`). See docs/decisions/0001-credential-generic-broker-api.md.
type PolicyFileV2 struct {
	SchemaVersion      string                   `json:"schema_version"`
	DefaultSessionTTL  string                   `json:"default_session_ttl"`
	DefaultIdleTimeout string                   `json:"default_idle_timeout"`
	Agents             map[string]AgentPolicyV2 `json:"agents"`
}

// AgentPolicyV2 is the v2 per-agent policy. Resources are the credential-
// generic replacement for v1's AllowedRepos; per-provider configuration
// lives in flat optional sections (currently only GitHub).
type AgentPolicyV2 struct {
	Resources []string           `json:"resources"`
	GitHub    *GitHubAgentConfig `json:"github,omitempty"`
}

// GitHubAgentConfig is the v2 per-agent GitHub configuration. AppID is
// optional; if omitted the broker's signer falls back to the identities
// file's default AppID for the agent. DefaultPermissions follows the same
// schema as v1's default_permissions map.
type GitHubAgentConfig struct {
	InstallationID     int64             `json:"installation_id"`
	AppID              string            `json:"app_id,omitempty"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}
