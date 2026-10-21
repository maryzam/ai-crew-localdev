package agents

type LoginKind string

const (
	Claude LoginKind = "claude-setup-token"
	Codex  LoginKind = "codex-device-auth"
)

type Agent struct {
	Name           string
	Version        string
	Package        string
	Login          LoginKind
	CredentialFile string
	LoginCommand   []string
}

const LoginImage = "localhost/ai-crew-login:claude-2.1.302-codex-0.163.0"

var registry = []Agent{
	{Name: "claude", Version: "2.1.302", Package: "@anthropic-ai/claude-code", Login: Claude, CredentialFile: "token", LoginCommand: []string{"claude", "setup-token"}},
	{Name: "codex", Version: "0.163.0", Package: "@openai/codex", Login: Codex, CredentialFile: "auth.json", LoginCommand: []string{"codex", "login", "--device-auth"}},
}

func All() []Agent {
	result := make([]Agent, len(registry))
	for index, agent := range registry {
		result[index] = agent
		result[index].LoginCommand = append([]string(nil), agent.LoginCommand...)
	}
	return result
}

func Find(name string) (Agent, bool) {
	for _, agent := range registry {
		if agent.Name == name {
			agent.LoginCommand = append([]string(nil), agent.LoginCommand...)
			return agent, true
		}
	}
	return Agent{}, false
}
