package managedrun

import (
	"io"

	"github.com/maryzam/ai-crew-localdev/internal/control"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/launcher"
)

type Request struct {
	AgentName                string
	TaskRef                  string
	RepoPath                 string
	BrokerSocketPathOverride string
	CredentialHelperPath     string
	GhWrapperPath            string
	VerifyCommand            string
	MaxRetries               int
	TokenWarnAt              int64
	TokenStopAt              int64
	IsolateHome              bool
	AgentCommand             []string
	ObservabilityResource    string
	AIAgentVersion           string
}

func Run(errOut io.Writer, request Request) error {
	planned, err := control.NewPlanner(errOut).PlanRun(control.RunRequest{
		AgentName:                request.AgentName,
		TaskRef:                  request.TaskRef,
		RepoPath:                 request.RepoPath,
		BrokerSocketPathOverride: request.BrokerSocketPathOverride,
		CredentialHelperPath:     request.CredentialHelperPath,
		GhWrapperPath:            request.GhWrapperPath,
		VerifyCommand:            request.VerifyCommand,
		MaxRetries:               request.MaxRetries,
		TokenWarnAt:              request.TokenWarnAt,
		TokenStopAt:              request.TokenStopAt,
		IsolateHome:              request.IsolateHome,
		AgentCommand:             request.AgentCommand,
		ObservabilityResource:    request.ObservabilityResource,
	})
	if err != nil {
		return err
	}
	return launcher.Launch(planned.Plan, launcher.Options{AIAgentVersion: request.AIAgentVersion})
}
