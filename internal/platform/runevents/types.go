package runevents

import (
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/modelattrib"
)

const SchemaVersion = "2.0"

type AgentMetadata = modelattrib.AgentMetadata
type ModelAttribution = modelattrib.ModelAttribution

type TaskMetadata struct {
	Type string `json:"type,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

type Usage struct {
	Status           string  `json:"status"`
	InputTokens      *int64  `json:"input_tokens,omitempty"`
	OutputTokens     *int64  `json:"output_tokens,omitempty"`
	CacheReadTokens  *int64  `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64  `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  *int64  `json:"reasoning_tokens,omitempty"`
	TotalTokens      *int64  `json:"total_tokens,omitempty"`
	CostAmount       *string `json:"cost_amount,omitempty"`
	CostCurrency     string  `json:"cost_currency,omitempty"`
	Source           string  `json:"source,omitempty"`
	Scope            string  `json:"scope,omitempty"`
	Precision        string  `json:"precision,omitempty"`
	Confidence       string  `json:"confidence,omitempty"`
}

type ExecutionSummary struct {
	AgentAttempts  int  `json:"agent_attempts"`
	VerifyEnabled  bool `json:"verify_enabled"`
	VerifyAttempts int  `json:"verify_attempts"`
	MaxRetries     int  `json:"max_retries"`
}

type VerificationSummary struct {
	Outcome       string           `json:"outcome"`
	CommandSHA256 string           `json:"command_sha256,omitempty"`
	LastExitCode  *int             `json:"last_exit_code,omitempty"`
	FailureClass  string           `json:"failure_class,omitempty"`
	Contracts     []ContractResult `json:"contracts,omitempty"`
}

type ContractResult struct {
	Name          string `json:"name"`
	Outcome       string `json:"outcome"`
	CommandSHA256 string `json:"command_sha256,omitempty"`
	FailureClass  string `json:"failure_class,omitempty"`
	LastExitCode  *int   `json:"last_exit_code,omitempty"`
	Attempts      int    `json:"attempts"`
}

type BrokerSummary struct {
	SessionID      string `json:"session_id,omitempty"`
	SessionCreated bool   `json:"session_created"`
	SessionRevoked bool   `json:"session_revoked"`
}

type RuntimeMetadata struct {
	AIAgentVersion   string `json:"ai_agent_version,omitempty"`
	OS               string `json:"os,omitempty"`
	Arch             string `json:"arch,omitempty"`
	Containerized    bool   `json:"containerized"`
	ContainerRuntime string `json:"container_runtime,omitempty"`
}

type DiagnosticMetadata struct {
	ErrorType    string `json:"error_type,omitempty"`
	ErrorSummary string `json:"error_summary,omitempty"`
	OutputPath   string `json:"output_path,omitempty"`
}

type RepositoryMetadata struct {
	Slug       string `json:"slug"`
	RemoteHost string `json:"remote_host,omitempty"`
	CommitSHA  string `json:"commit_sha,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Dirty      bool   `json:"dirty"`
	RootPath   string `json:"root_path,omitempty"`
}

type RunSummary struct {
	SchemaVersion string              `json:"schema_version"`
	RunID         string              `json:"run_id"`
	TraceID       string              `json:"trace_id"`
	StartedAt     time.Time           `json:"started_at"`
	EndedAt       *time.Time          `json:"ended_at,omitempty"`
	DurationMS    int64               `json:"duration_ms,omitempty"`
	Mode          string              `json:"mode"`
	Outcome       string              `json:"outcome,omitempty"`
	TerminalPhase string              `json:"terminal_phase,omitempty"`
	ExitCode      *int                `json:"exit_code,omitempty"`
	Signal        string              `json:"signal,omitempty"`
	Task          TaskMetadata        `json:"task"`
	Repository    RepositoryMetadata  `json:"repository"`
	Agent         AgentMetadata       `json:"agent"`
	Model         ModelAttribution    `json:"model"`
	Execution     ExecutionSummary    `json:"execution"`
	Verification  VerificationSummary `json:"verification"`
	Usage         *Usage              `json:"usage,omitempty"`
	Broker        BrokerSummary       `json:"broker"`
	Runtime       RuntimeMetadata     `json:"runtime"`
	Diagnostics   DiagnosticMetadata  `json:"diagnostics"`
}

type Event struct {
	SchemaVersion string            `json:"schema_version"`
	Timestamp     time.Time         `json:"timestamp"`
	EventType     string            `json:"event_type"`
	Phase         string            `json:"phase,omitempty"`
	Attempt       int               `json:"attempt,omitempty"`
	Outcome       string            `json:"outcome,omitempty"`
	ExitCode      *int              `json:"exit_code,omitempty"`
	DurationMS    int64             `json:"duration_ms,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Run           RunSummary        `json:"run"`
}
