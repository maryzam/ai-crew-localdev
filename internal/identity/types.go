package identity

type IdentitiesFile struct {
	SchemaVersion string                   `json:"schema_version"`
	Agents        map[string]AgentIdentity `json:"agents"`
}

type AgentIdentity struct {
	GitName        string `json:"git_name"`
	GitEmail       string `json:"git_email"`
	GithubHost     string `json:"github_host"`
	AppID          string `json:"app_id"`
	AppKey         string `json:"app_key"`
	InstallationID *int64 `json:"installation_id,omitempty"`
	Tool           string `json:"tool"`
	Model          string `json:"model"`
}
