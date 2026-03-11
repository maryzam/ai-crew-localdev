package identity

// IdentitiesFile represents the top-level identities configuration file.
type IdentitiesFile struct {
	SchemaVersion string                   `json:"schema_version"`
	Agents        map[string]AgentIdentity `json:"agents"`
}

// AgentIdentity represents configuration for a single AI agent.
type AgentIdentity struct {
	GitName    string `json:"git_name"`
	GitEmail   string `json:"git_email"`
	GithubHost string `json:"github_host"`
	AppID      string `json:"app_id"`
	AppKey     string `json:"app_key"`
	Tool       string `json:"tool"`
	Model      string `json:"model"`
}
