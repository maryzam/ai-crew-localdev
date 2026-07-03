package agentauth

import (
	"context"
	"encoding/json"
	"strings"
)

type Status string

const (
	StatusLoggedIn     Status = "logged_in"
	StatusLoggedOut    Status = "logged_out"
	StatusNotInstalled Status = "not_installed"
	StatusUnknown      Status = "unknown"
)

const (
	claudeBinary = "claude"
	codexBinary  = "codex"
)

const (
	claudeRemediation = "run 'claude' or 'claude auth login' once inside this devcontainer; login persists in /home/dev across container replacement"
	codexRemediation  = "run 'codex login' (or 'codex login --with-api-key') once inside this devcontainer; login persists in /home/dev across container replacement"
	installClaudeHint = "install the Claude Code CLI (npm install -g @anthropic-ai/claude-code) or use the generic devcontainer where it is preinstalled"
	installCodexHint  = "install the Codex CLI (npm install -g @openai/codex) or use the generic devcontainer where it is preinstalled"
	brokerReminder    = "keep GitHub access on the brokered path; do not run 'gh auth login' in the container"
)

type AgentReport struct {
	Agent       string `json:"agent"`
	Status      Status `json:"status"`
	Method      string `json:"method,omitempty"`
	Source      string `json:"source,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

type Report struct {
	Agents []AgentReport `json:"agents"`
}

func (r Report) AllLoggedIn() bool {
	for _, agent := range r.Agents {
		if agent.Status != StatusLoggedIn {
			return false
		}
	}
	return len(r.Agents) > 0
}

type ProbeResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Err      error
}

type Dependencies struct {
	FindBinary func(name string) (string, error)
	Run        func(ctx context.Context, name string, args ...string) ProbeResult
}

type Service struct {
	deps Dependencies
}

func New(deps Dependencies) Service {
	return Service{deps: deps}
}

func (s Service) Status(ctx context.Context) Report {
	return Report{Agents: []AgentReport{
		s.claudeStatus(ctx),
		s.codexStatus(ctx),
	}}
}

type claudeStatusJSON struct {
	LoggedIn     bool   `json:"loggedIn"`
	AuthMethod   string `json:"authMethod"`
	APIKeySource string `json:"apiKeySource"`
	APIProvider  string `json:"apiProvider"`
}

func (s Service) claudeStatus(ctx context.Context) AgentReport {
	report := AgentReport{Agent: claudeBinary}
	if _, err := s.deps.FindBinary(claudeBinary); err != nil {
		report.Status = StatusNotInstalled
		report.Detail = err.Error()
		report.Remediation = installClaudeHint
		return report
	}
	result := s.deps.Run(ctx, claudeBinary, "auth", "status", "--json")
	var parsed claudeStatusJSON
	if err := json.Unmarshal(result.Stdout, &parsed); err != nil {
		report.Status = StatusUnknown
		report.Detail = probeFailureDetail(result, err)
		report.Remediation = claudeRemediation
		return report
	}
	report.Method = parsed.AuthMethod
	report.Source = parsed.APIKeySource
	if parsed.LoggedIn {
		report.Status = StatusLoggedIn
		return report
	}
	report.Status = StatusLoggedOut
	report.Remediation = claudeRemediation
	return report
}

func (s Service) codexStatus(ctx context.Context) AgentReport {
	report := AgentReport{Agent: codexBinary}
	if _, err := s.deps.FindBinary(codexBinary); err != nil {
		report.Status = StatusNotInstalled
		report.Detail = err.Error()
		report.Remediation = installCodexHint
		return report
	}
	result := s.deps.Run(ctx, codexBinary, "login", "status")
	output := string(result.Stdout) + string(result.Stderr)
	switch {
	case strings.Contains(output, "Logged in"):
		report.Status = StatusLoggedIn
		report.Method = codexMethod(output)
	case strings.Contains(output, "Not logged in"):
		report.Status = StatusLoggedOut
		report.Remediation = codexRemediation
	default:
		report.Status = StatusUnknown
		report.Detail = probeFailureDetail(result, nil)
		report.Remediation = codexRemediation
	}
	return report
}

func codexMethod(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Logged in") {
			return trimmed
		}
	}
	return ""
}

func probeFailureDetail(result ProbeResult, parseErr error) string {
	if parseErr != nil {
		if stderr := strings.TrimSpace(string(result.Stderr)); stderr != "" {
			return stderr
		}
		return "could not parse login status: " + parseErr.Error()
	}
	if result.Err != nil {
		return result.Err.Error()
	}
	if stderr := strings.TrimSpace(string(result.Stderr)); stderr != "" {
		return stderr
	}
	return "login status probe returned unrecognized output"
}
