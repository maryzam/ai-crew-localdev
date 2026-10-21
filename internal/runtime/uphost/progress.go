package uphost

type ProgressKind string

const (
	GenericLaunching       ProgressKind = "generic-launching"
	GenericReady           ProgressKind = "generic-ready"
	ProjectLaunching       ProgressKind = "project-launching"
	ProjectBootstrapFailed ProgressKind = "project-bootstrap-failed"
	ProjectReady           ProgressKind = "project-ready"
	AuthStatusChecking     ProgressKind = "auth-status-checking"
	AuthStatusFailed       ProgressKind = "auth-status-failed"
	ShellOpening           ProgressKind = "shell-opening"
	AgentOpening           ProgressKind = "agent-opening"
	ManagedWorkspaceReady  ProgressKind = "managed-workspace-ready"
	PhoenixStarting        ProgressKind = "phoenix-starting"
	PhoenixReady           ProgressKind = "phoenix-ready"
)

type Progress struct {
	Kind      ProgressKind
	Target    string
	Workspace string
	Runtime   string
	Command   string
	Err       error
}

type ProgressFunc func(Progress)
