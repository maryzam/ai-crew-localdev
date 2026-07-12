package contract

import "time"

const CredentialType = "github_app_installation"

type Credential struct {
	Token string `json:"token"`
}

type Params struct {
	Permissions map[string]string `json:"permissions,omitempty"`
}

type PolicySection struct {
	InstallationID     int64             `json:"installation_id"`
	AppID              string            `json:"app_id,omitempty"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}

type InstallationToken struct {
	Token     string
	ExpiresAt time.Time
	Repo      string
}

type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
	RepositorySelection string `json:"repository_selection"`
}

type Repository struct {
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}
