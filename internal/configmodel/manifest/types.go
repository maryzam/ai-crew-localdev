package manifest

type File struct {
	SchemaVersion string     `json:"schema_version"`
	Contracts     []Contract `json:"contracts,omitempty"`
	Agents        *Agents    `json:"agents,omitempty"`
}

type Contract struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Retry   string `json:"retry,omitempty"`
}

type Agents struct {
	Allowed  []string                 `json:"allowed,omitempty"`
	Defaults map[string]AgentDefaults `json:"defaults,omitempty"`
}

type AgentDefaults struct {
	Model string `json:"model,omitempty"`
}

const (
	RetryAgent = "agent"
	RetryNever = "never"
)
