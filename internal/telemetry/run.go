package telemetry

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/config"
)

const (
	defaultLangfuseHost = "http://localhost:3000"
	langfuseIngestPath  = "/api/public/ingestion"
)

type RunContext struct {
	RunID         string
	AgentName     string
	Repo          string
	HostRepoPath  string
	AgentCommand  []string
	VerifyEnabled bool
	AuditLogPath  string
}

type Usage struct {
	InputTokens  string `json:"input_tokens"`
	OutputTokens string `json:"output_tokens"`
	TotalTokens  string `json:"total_tokens"`
	CostUSD      string `json:"cost_usd"`
}

type Event struct {
	Timestamp  time.Time         `json:"timestamp"`
	RunID      string            `json:"run_id"`
	SessionID  string            `json:"session_id,omitempty"`
	EventType  string            `json:"event_type"`
	AgentName  string            `json:"agent_name"`
	Repo       string            `json:"repo"`
	Model      string            `json:"model,omitempty"`
	Attempt    int               `json:"attempt,omitempty"`
	Outcome    string            `json:"outcome,omitempty"`
	ExitCode   *int              `json:"exit_code,omitempty"`
	DurationMS int64             `json:"duration_ms,omitempty"`
	Usage      *Usage            `json:"usage,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type Recorder struct {
	mu        sync.Mutex
	run       RunContext
	sessionID string
	model     string
	started   time.Time
	local     *localSink
	langfuse  *langfuseSink
}

func NewRunID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return "run_" + hex.EncodeToString(b), nil
}

func StartRun(ctx RunContext) (*Recorder, error) {
	if telemetryDisabled() {
		return noopRecorder(ctx), nil
	}
	if ctx.RunID == "" {
		runID, err := NewRunID()
		if err != nil {
			return nil, err
		}
		ctx.RunID = runID
	}
	if ctx.AuditLogPath == "" {
		ctx.AuditLogPath = auditLogPath()
	}

	local, err := newLocalSink(localTelemetryPath())
	if err != nil {
		return nil, err
	}
	rec := &Recorder{
		run:      ctx,
		model:    inferModel(ctx.AgentCommand),
		started:  time.Now().UTC(),
		local:    local,
		langfuse: newLangfuseSinkFromEnv(),
	}
	rec.record("run.started", 0, "", nil, 0, map[string]string{
		"host_repo_path":            ctx.HostRepoPath,
		"agent_command":             safeCommandName(ctx.AgentCommand),
		"agent_command_arg_count":   strconv.Itoa(max(len(ctx.AgentCommand)-1, 0)),
		"verify_enabled":            strconv.FormatBool(ctx.VerifyEnabled),
		"local_telemetry_path":      local.path,
		"audit_log_path":            ctx.AuditLogPath,
		"langfuse_ingestion_config": langfuseConfigState(rec.langfuse),
	}, true)
	return rec, nil
}

func noopRecorder(ctx RunContext) *Recorder {
	return &Recorder{run: ctx, started: time.Now().UTC()}
}

func (r *Recorder) RunID() string {
	if r == nil {
		return ""
	}
	return r.run.RunID
}

func (r *Recorder) SetSessionID(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.sessionID = sessionID
	r.mu.Unlock()
	r.record("session.created", 0, "", nil, 0, map[string]string{
		"broker_audit_correlation": "run_id",
	}, false)
}

func (r *Recorder) AgentStarted(attempt int) {
	r.record("agent.command.started", attempt, "", nil, 0, map[string]string{
		"agent_command": safeCommandName(r.run.AgentCommand),
	}, false)
}

func (r *Recorder) AgentFinished(attempt int, outcome string, exitCode *int, duration time.Duration) {
	r.record("agent.command.finished", attempt, outcome, exitCode, duration, nil, false)
}

func (r *Recorder) VerifyStarted(attempt int, verifyCmd string) {
	r.record("verify.command.started", attempt, "", nil, 0, map[string]string{
		"verify_command_sha256": sha256Hex(verifyCmd),
	}, false)
}

func (r *Recorder) VerifyFinished(attempt int, outcome string, exitCode *int, duration time.Duration) {
	r.record("verify.command.finished", attempt, outcome, exitCode, duration, nil, false)
}

func (r *Recorder) UsageUnknown() {
	r.record("usage.recorded", 0, "unknown", nil, 0, nil, false)
}

func (r *Recorder) Finished(outcome string, exitCode *int, retryCount int, duration time.Duration) {
	if duration == 0 {
		duration = time.Since(r.started)
	}
	r.record("run.finished", 0, outcome, exitCode, duration, map[string]string{
		"retry_count": strconv.Itoa(retryCount),
	}, false)
}

func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	if r.langfuse != nil {
		r.langfuse.close()
	}
	if r.local == nil {
		return nil
	}
	return r.local.close()
}

func (r *Recorder) record(eventType string, attempt int, outcome string, exitCode *int, duration time.Duration, metadata map[string]string, createTrace bool) {
	if r == nil || r.run.RunID == "" {
		return
	}
	r.mu.Lock()
	ev := Event{
		Timestamp:  time.Now().UTC(),
		RunID:      r.run.RunID,
		SessionID:  r.sessionID,
		EventType:  eventType,
		AgentName:  r.run.AgentName,
		Repo:       r.run.Repo,
		Model:      r.model,
		Attempt:    attempt,
		Outcome:    outcome,
		ExitCode:   exitCode,
		DurationMS: duration.Milliseconds(),
		Metadata:   metadata,
	}
	if eventType == "usage.recorded" {
		ev.Usage = &Usage{
			InputTokens:  "unknown",
			OutputTokens: "unknown",
			TotalTokens:  "unknown",
			CostUSD:      "unknown",
		}
	}
	local := r.local
	lf := r.langfuse
	r.mu.Unlock()

	if local != nil {
		_ = local.write(ev)
	}
	if lf != nil {
		lf.enqueue(ev, createTrace)
	}
}

func telemetryDisabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AI_AGENT_TELEMETRY")))
	return v == "0" || v == "false" || v == "off" || v == "disabled"
}

func localTelemetryPath() string {
	if path := strings.TrimSpace(os.Getenv("AI_AGENT_RUN_TELEMETRY_LOG")); path != "" {
		return config.ExpandHome(path)
	}
	return config.DefaultRunTelemetryPath()
}

func auditLogPath() string {
	if path := strings.TrimSpace(os.Getenv("AI_AGENT_AUDIT_LOG")); path != "" {
		return config.ExpandHome(path)
	}
	return config.DefaultAuditLogPath()
}

func safeCommandName(command []string) string {
	if len(command) == 0 {
		return ""
	}
	return filepath.Base(command[0])
}

func inferModel(command []string) string {
	for i, arg := range command {
		if arg == "--model" && i+1 < len(command) {
			return command[i+1]
		}
		if value, ok := strings.CutPrefix(arg, "--model="); ok {
			return value
		}
	}
	for _, key := range []string{"AI_AGENT_MODEL", "OPENAI_MODEL", "ANTHROPIC_MODEL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return "unknown"
}

func langfuseConfigState(sink *langfuseSink) string {
	if sink == nil {
		return "not_configured"
	}
	return "configured"
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
