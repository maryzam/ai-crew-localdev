package agents

import "maps"

type LoginKind string

const (
	Claude LoginKind = "claude-setup-token"
	Codex  LoginKind = "codex-device-auth"
)

type Agent struct {
	Name            string
	Version         string
	Package         string
	Login           LoginKind
	LoginCommand    []string
	CredentialFile  string
	CredentialLimit int64
	Refresh         *RefreshBinary
}

type RefreshBinary struct {
	ImagePath string
	FileName  string
	SizeLimit int64
	SHA256    map[string]string
}

func (a Agent) PackageSpec() string {
	return a.Package + "@" + a.Version
}

type Registry struct {
	agents []Agent
}

func NewRegistry(declared ...Agent) Registry {
	registry := Registry{agents: make([]Agent, len(declared))}
	for index, agent := range declared {
		registry.agents[index] = agent.clone()
	}
	return registry
}

func Default() Registry {
	return NewRegistry(
		Agent{Name: "claude", Version: "2.1.302", Package: "@anthropic-ai/claude-code", Login: Claude, LoginCommand: []string{"claude", "setup-token"}, CredentialFile: "token", CredentialLimit: 4 << 10},
		Agent{Name: "codex", Version: "0.163.0", Package: "@openai/codex", Login: Codex, LoginCommand: []string{"codex", "-c", `cli_auth_credentials_store="file"`, "login", "--device-auth"}, CredentialFile: "auth.json", CredentialLimit: 256 << 10, Refresh: &RefreshBinary{
			ImagePath: "/opt/ai-crew/codex",
			FileName:  "codex",
			SizeLimit: 512 << 20,
			SHA256: map[string]string{
				"amd64": "399ff69c431c066e40de318fba45c2b07f45a76b19939d247ee4828fc70c4215",
				"arm64": "8a96f66d059d653e68bfd5f3c79337e6f1d7d170d80bf705eb7d0249b34c0ece",
			},
		}},
	)
}

func (r Registry) All() []Agent {
	result := make([]Agent, len(r.agents))
	for index, agent := range r.agents {
		result[index] = agent.clone()
	}
	return result
}

func (r Registry) Names() []string {
	names := make([]string, len(r.agents))
	for index, agent := range r.agents {
		names[index] = agent.Name
	}
	return names
}

func (r Registry) Find(name string) (Agent, bool) {
	for _, agent := range r.agents {
		if agent.Name == name {
			return agent.clone(), true
		}
	}
	return Agent{}, false
}

func (a Agent) clone() Agent {
	a.LoginCommand = append([]string(nil), a.LoginCommand...)
	if a.Refresh != nil {
		refresh := *a.Refresh
		refresh.SHA256 = maps.Clone(a.Refresh.SHA256)
		a.Refresh = &refresh
	}
	return a
}
