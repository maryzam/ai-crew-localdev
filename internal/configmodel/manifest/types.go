package manifest

type File struct {
	SchemaVersion   string           `json:"schema_version"`
	Contracts       []Contract       `json:"contracts,omitempty"`
	Agents          *Agents          `json:"agents,omitempty"`
	Resources       []Resource       `json:"resources,omitempty"`
	Caches          []Cache          `json:"caches,omitempty"`
	Services        []Service        `json:"services,omitempty"`
	Ports           []Port           `json:"ports,omitempty"`
	RunModes        []string         `json:"run_modes,omitempty"`
	ResourceBudgets []ResourceBudget `json:"resource_budgets,omitempty"`
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

type Resource struct {
	URI string `json:"uri"`
}

type Cache struct {
	Name     string `json:"name"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type Service struct {
	Name string `json:"name"`
}

type Port struct {
	Number int `json:"number"`
}

type ResourceBudget struct {
	Name              string `json:"name"`
	Metric            string `json:"metric"`
	MeasurementSource string `json:"measurement_source,omitempty"`
	WarnAt            int64  `json:"warn_at,omitempty"`
	StopAt            int64  `json:"stop_at,omitempty"`
	StopPolicy        string `json:"stop_policy,omitempty"`
}

const (
	RetryAgent = "agent"
	RetryNever = "never"

	RunModeManagedRun          = "managed_run"
	RunModeProjectDevcontainer = "project_devcontainer"

	BudgetMetricTokens          = "tokens"
	BudgetMeasurementNativeOTEL = "native_otel"
	BudgetStopPolicyWarnOnly    = "warn_only"
	BudgetStopPolicyStopRun     = "stop_run"
)
