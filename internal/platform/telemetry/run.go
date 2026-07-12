package telemetry

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/correlation"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

var localWarnings io.Writer = os.Stderr

const (
	OutcomePassed              = "passed"
	OutcomeAgentFailed         = "agent_failed"
	OutcomeVerifyFailed        = "verify_failed"
	OutcomeLaunchFailed        = "launch_failed"
	OutcomeSessionCreateFailed = "session_create_failed"
	OutcomeInterrupted         = "interrupted"
)

const (
	PhaseResolveRepo   = "resolve_repo"
	PhaseSessionCreate = "session_create"
	PhaseBindSetup     = "bind_setup"
	PhaseWrapperSetup  = "wrapper_setup"
	PhaseAgentStart    = "agent_start"
	PhaseAgent         = "agent"
	PhaseVerify        = "verify"
	PhaseCleanup       = "cleanup"
)

type RunContext struct {
	RunID           string
	TaskRef         string
	AgentName       string
	Agent           AgentMetadata
	ConfiguredModel string
	Model           ModelAttribution
	Repo            string
	HostRepoPath    string
	AgentCommand    []string
	VerifyEnabled   bool
	MaxRetries      int
	AIAgentVersion  string
	AuditLogPath    string
}

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

type Recorder struct {
	mu        sync.Mutex
	run       RunContext
	summary   RunSummary
	local     *localSink
	otlp      *otlpSink
	disabled  bool
	finished  bool
	closeOnce sync.Once
	warnOnce  sync.Once
	closeErr  error
}

func NewRunID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return "run_" + hex.EncodeToString(b), nil
}

func StartRun(ctx RunContext) (*Recorder, error) {
	disabled := disabledRecorder(ctx)
	if err := correlation.ValidateTaskRef(ctx.TaskRef); err != nil {
		return disabled, err
	}
	if ctx.RunID == "" {
		runID, err := NewRunID()
		if err != nil {
			return disabled, err
		}
		ctx.RunID = runID
	}
	disabled.run = ctx
	if err := correlation.ValidateRunID(ctx.RunID); err != nil {
		return disabled, err
	}
	if ctx.AuditLogPath == "" {
		ctx.AuditLogPath = auditLogPath()
	}

	if telemetryDisabled() {
		return disabled, nil
	}

	agent, model := plannedAgentModel(ctx)
	started := time.Now().UTC()
	summary := RunSummary{
		SchemaVersion: SchemaVersion,
		RunID:         ctx.RunID,
		TraceID:       traceIDForRun(ctx.RunID),
		StartedAt:     started,
		Mode:          runMode(ctx.TaskRef),
		Task:          TaskMetadata{Type: taskType(ctx.TaskRef), Ref: ctx.TaskRef},
		Repository:    inspectRepository(ctx.HostRepoPath, ctx.Repo),
		Agent:         agent,
		Model:         model,
		Execution: ExecutionSummary{
			VerifyEnabled: ctx.VerifyEnabled,
			MaxRetries:    ctx.MaxRetries,
		},
		Verification: VerificationSummary{Outcome: "not_run"},
		Runtime:      inspectRuntime(ctx.AIAgentVersion),
	}

	local, err := newLocalSink(localTelemetryPath())
	if err != nil {
		return disabled, err
	}
	rec := &Recorder{
		run:     ctx,
		summary: summary,
		local:   local,
	}
	rec.record("run.started", PhaseSessionCreate, 0, "", nil, 0, map[string]string{
		"agent_command":            safeCommandName(ctx.AgentCommand),
		"agent_command_arg_count":  strconv.Itoa(max(len(ctx.AgentCommand)-1, 0)),
		"local_telemetry_path":     local.path,
		"audit_log_path":           ctx.AuditLogPath,
		"remote_export_configured": strconv.FormatBool(rec.otlp != nil),
	})
	return rec, nil
}

func plannedAgentModel(ctx RunContext) (AgentMetadata, ModelAttribution) {
	if ctx.Agent.Type != "" && ctx.Model.Resolution.Status != "" {
		agent := ctx.Agent
		if agent.Identity == "" {
			agent.Identity = boundedField("ai_agent.agent.identity", ctx.AgentName)
		}
		if agent.Command == "" {
			agent.Command = safeCommandName(ctx.AgentCommand)
		}
		return agent, ctx.Model
	}
	return ResolveAgentModelWithConfig(ctx.AgentName, ctx.ConfiguredModel, ctx.AgentCommand)
}

func disabledRecorder(ctx RunContext) *Recorder {
	return &Recorder{run: ctx, disabled: true}
}

func (r *Recorder) ConfigureOTLP(exporter OTLPExporter) error {
	if r.disabled {
		return nil
	}
	sink, err := newOTLPSink(exporter)
	if err != nil {
		return err
	}
	return r.configureOTLPSink(sink)
}

func (r *Recorder) configureOTLPSink(sink *otlpSink) error {
	r.mu.Lock()
	if r.otlp != nil {
		r.mu.Unlock()
		return fmt.Errorf("OTLP exporter already configured")
	}
	r.otlp = sink
	started := Event{
		SchemaVersion: SchemaVersion,
		Timestamp:     r.summary.StartedAt,
		EventType:     "run.started",
		Phase:         PhaseSessionCreate,
		Run:           cloneSummary(r.summary),
	}
	r.mu.Unlock()
	sink.enqueue(started)
	r.record("telemetry.configured", PhaseSessionCreate, 0, "", nil, 0, nil)
	return nil
}

func (r *Recorder) RunID() string {
	return r.run.RunID
}

func (r *Recorder) SetSessionID(sessionID string) {
	if r.disabled {
		return
	}
	r.mu.Lock()
	r.summary.Broker.SessionID = sessionID
	r.summary.Broker.SessionCreated = true
	r.mu.Unlock()
	r.record("session.created", PhaseSessionCreate, 0, "", nil, 0, nil)
}

func (r *Recorder) SessionRevoked() {
	if r.disabled {
		return
	}
	r.mu.Lock()
	r.summary.Broker.SessionRevoked = true
	r.mu.Unlock()
	r.record("session.revoked", PhaseCleanup, 0, "", nil, 0, nil)
}

func (r *Recorder) AgentStarted(attempt int) {
	if r.disabled {
		return
	}
	r.updateAttempt(attempt, false)
	r.record("agent.command.started", PhaseAgent, attempt, "", nil, 0, nil)
}

func (r *Recorder) AgentFinished(attempt int, outcome string, exitCode *int, duration time.Duration) {
	if r.disabled {
		return
	}
	r.record("agent.command.finished", PhaseAgent, attempt, outcome, exitCode, duration, nil)
}

func (r *Recorder) VerifyStarted(attempt int, contract string, verifyCmd string) {
	if r.disabled {
		return
	}
	r.updateAttempt(attempt, true)
	hash := sha256Hex(verifyCmd)
	r.mu.Lock()
	r.summary.Verification.CommandSHA256 = hash
	r.mu.Unlock()
	detail := map[string]string{"command_sha256": hash}
	if contract != "" {
		detail["contract"] = bounded(contract, MaxPropagatedValueLength)
	}
	r.record("verify.attempt.started", PhaseVerify, attempt, "", nil, 0, detail)
}

func (r *Recorder) VerifyFinished(attempt int, contract string, outcome string, failureClass string, exitCode *int, duration time.Duration) {
	if r.disabled {
		return
	}
	r.mu.Lock()
	r.summary.Verification.Outcome = outcome
	r.summary.Verification.LastExitCode = cloneInt(exitCode)
	r.summary.Verification.FailureClass = failureClass
	hash := r.summary.Verification.CommandSHA256
	if contract != "" {
		r.updateContractResult(contract, outcome, failureClass, hash, exitCode)
	}
	r.mu.Unlock()
	detail := map[string]string{"command_sha256": hash}
	if contract != "" {
		detail["contract"] = bounded(contract, MaxPropagatedValueLength)
	}
	if failureClass != "" {
		detail["failure_class"] = failureClass
	}
	r.record("verify.attempt.finished", PhaseVerify, attempt, outcome, exitCode, duration, detail)
}

func (r *Recorder) updateContractResult(name, outcome, failureClass, hash string, exitCode *int) {
	for i := range r.summary.Verification.Contracts {
		if r.summary.Verification.Contracts[i].Name != name {
			continue
		}
		r.summary.Verification.Contracts[i].Outcome = outcome
		r.summary.Verification.Contracts[i].FailureClass = failureClass
		r.summary.Verification.Contracts[i].CommandSHA256 = hash
		r.summary.Verification.Contracts[i].LastExitCode = cloneInt(exitCode)
		r.summary.Verification.Contracts[i].Attempts++
		return
	}
	r.summary.Verification.Contracts = append(r.summary.Verification.Contracts, ContractResult{
		Name:          name,
		Outcome:       outcome,
		FailureClass:  failureClass,
		CommandSHA256: hash,
		LastExitCode:  cloneInt(exitCode),
		Attempts:      1,
	})
}

func (r *Recorder) ObserveModel(model, provider, source string) {
	if r.disabled || strings.TrimSpace(model) == "" {
		return
	}
	model = bounded(model, MaxPropagatedValueLength)
	if strings.TrimSpace(source) == "" {
		source = "agent_telemetry"
	}
	r.mu.Lock()
	r.summary.Model.Observed = model
	r.summary.Model.Provider = boundedField("gen_ai.provider.name", firstNonEmpty(provider, providerForModel(model), r.summary.Model.Provider))
	r.summary.Model.Family = boundedField("ai_agent.model.family", firstNonEmpty(familyForModel(model), r.summary.Model.Family))
	r.summary.Model.Resolution.Status = "resolved"
	r.summary.Model.Resolution.Confidence = "observed"
	r.summary.Model.Resolution.PrimarySource = bounded(source, 32)
	if !slicesContains(r.summary.Model.Resolution.Sources, source) {
		r.summary.Model.Resolution.Sources = append(r.summary.Model.Resolution.Sources, source)
	}
	if r.summary.Model.Requested != "" && !strings.EqualFold(r.summary.Model.Requested, model) {
		r.summary.Model.Resolution.Conflict = true
	}
	r.mu.Unlock()
	r.record("model.resolved", PhaseAgent, 0, "", nil, 0, nil)
}

func (r *Recorder) RecordUsage(usage Usage) {
	if r.disabled || usage.Status == "unavailable" {
		return
	}
	r.mu.Lock()
	r.summary.Usage = cloneUsage(&usage)
	r.mu.Unlock()
	r.record("usage.recorded", PhaseAgent, 0, usage.Status, nil, 0, nil)
}

func (r *Recorder) SetDiagnostic(errorType, summary string) {
	if r.disabled {
		return
	}
	r.mu.Lock()
	r.summary.Diagnostics.ErrorType = bounded(errorType, 64)
	r.summary.Diagnostics.ErrorSummary = bounded(summary, 512)
	r.mu.Unlock()
	r.record("diagnostic.recorded", PhaseCleanup, 0, "", nil, 0, nil)
}

func (r *Recorder) SetSignal(signal string) {
	if r.disabled {
		return
	}
	r.mu.Lock()
	r.summary.Signal = bounded(signal, 32)
	r.mu.Unlock()
}

func (r *Recorder) Finish(outcome, phase string, exitCode *int, duration time.Duration) bool {
	if r.disabled {
		return false
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return false
	}
	r.finished = true
	ended := time.Now().UTC()
	if duration <= 0 {
		duration = ended.Sub(r.summary.StartedAt)
	}
	r.summary.EndedAt = &ended
	r.summary.DurationMS = duration.Milliseconds()
	r.summary.Outcome = outcome
	r.summary.TerminalPhase = phase
	r.summary.ExitCode = cloneInt(exitCode)
	r.mu.Unlock()
	r.record("run.finished", phase, 0, outcome, exitCode, duration, nil)
	return true
}

func (r *Recorder) Finished() bool {
	if r.disabled {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finished
}

func (r *Recorder) Summary() RunSummary {
	if r.disabled {
		return RunSummary{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSummary(r.summary)
}

func (r *Recorder) Close() error {
	if r.disabled {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.otlp != nil {
			r.otlp.close()
		}
		if r.local != nil {
			if err := r.local.close(); err != nil {
				r.setLocalError(err)
			}
		}
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closeErr
}

func (r *Recorder) CloseOTLP() {
	if r.disabled {
		return
	}
	r.mu.Lock()
	sink := r.otlp
	r.mu.Unlock()
	if sink != nil {
		sink.close()
	}
}

func (r *Recorder) updateAttempt(attempt int, verify bool) {
	if r.disabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if verify {
		r.summary.Execution.VerifyAttempts = max(r.summary.Execution.VerifyAttempts, attempt)
		return
	}
	r.summary.Execution.AgentAttempts = max(r.summary.Execution.AgentAttempts, attempt)
}

func (r *Recorder) record(eventType, phase string, attempt int, outcome string, exitCode *int, duration time.Duration, metadata map[string]string) {
	if r.disabled || r.run.RunID == "" {
		return
	}
	r.mu.Lock()
	event := Event{
		SchemaVersion: SchemaVersion,
		Timestamp:     time.Now().UTC(),
		EventType:     eventType,
		Phase:         phase,
		Attempt:       attempt,
		Outcome:       outcome,
		ExitCode:      cloneInt(exitCode),
		DurationMS:    duration.Milliseconds(),
		Metadata:      metadata,
		Run:           cloneSummary(r.summary),
	}
	local := r.local
	otlp := r.otlp
	r.mu.Unlock()

	if local != nil {
		if err := local.write(event); err != nil {
			r.setLocalError(err)
			r.warnOnce.Do(func() {
				_, _ = fmt.Fprintf(localWarnings, "warning: local managed-run telemetry failed: %v\n", err)
			})
		}
	}
	if otlp != nil {
		otlp.enqueue(event)
	}
}

func (r *Recorder) setLocalError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closeErr == nil {
		r.closeErr = err
	}
}

func telemetryDisabled() bool {
	return paths.TelemetryDisabled()
}

func LocalTelemetryPath() string {
	return paths.RunTelemetryLogPath()
}

func localTelemetryPath() string {
	return LocalTelemetryPath()
}

func auditLogPath() string {
	return paths.AuditLogPath()
}

func safeCommandName(command []string) string {
	if len(command) == 0 {
		return ""
	}
	return filepath.Base(command[0])
}

func runMode(taskRef string) string {
	if taskRef == "" {
		return "adhoc"
	}
	return "task"
}

func taskType(taskRef string) string {
	if strings.HasPrefix(taskRef, "github:") {
		return "github_issue"
	}
	if taskRef != "" {
		return "external"
	}
	return ""
}

func inspectRuntime(version string) RuntimeMetadata {
	metadata := RuntimeMetadata{AIAgentVersion: version, OS: runtime.GOOS, Arch: runtime.GOARCH}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		metadata.Containerized = true
		metadata.ContainerRuntime = "podman"
	} else if _, err := os.Stat("/.dockerenv"); err == nil {
		metadata.Containerized = true
		metadata.ContainerRuntime = "docker"
	}
	return metadata
}

func traceIDForRun(runID string) string {
	return sha256Hex(runID)[:32]
}

func ShortRunID(runID string) string {
	value := strings.TrimPrefix(runID, "run_")
	if len(value) > 8 {
		return value[:8]
	}
	return value
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneUsage(value *Usage) *Usage {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneSummary(value RunSummary) RunSummary {
	value.ExitCode = cloneInt(value.ExitCode)
	value.Verification.LastExitCode = cloneInt(value.Verification.LastExitCode)
	value.Usage = cloneUsage(value.Usage)
	value.Model.Resolution.Sources = append([]string(nil), value.Model.Resolution.Sources...)
	return value
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
