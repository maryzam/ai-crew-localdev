package uphost

type ProgressKind string

const (
	GenericLaunching       ProgressKind = "generic-launching"
	GenericReady           ProgressKind = "generic-ready"
	ProjectLaunching       ProgressKind = "project-launching"
	ProjectBootstrapFailed ProgressKind = "project-bootstrap-failed"
	ProjectReady           ProgressKind = "project-ready"
	ShellOpening           ProgressKind = "shell-opening"
	LangfuseEnvironment    ProgressKind = "langfuse-environment"
	LangfuseStarting       ProgressKind = "langfuse-starting"
	LangfuseReady          ProgressKind = "langfuse-ready"
)

type Progress struct {
	Kind      ProgressKind
	Target    string
	Workspace string
	Runtime   string
	Command   string
	Err       error
}

type ProgressSink interface {
	Report(Progress)
}

type ProgressFunc func(Progress)

func (f ProgressFunc) Report(progress Progress) {
	f(progress)
}
