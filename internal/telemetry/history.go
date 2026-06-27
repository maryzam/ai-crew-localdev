package telemetry

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

func ReadRunHistory(path string) ([]RunSummary, error) {
	runs := make(map[string]RunSummary)
	readAny := false
	for _, candidate := range []string{path + ".1", path} {
		file, err := os.Open(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read run history %s: %w", candidate, err)
		}
		readAny = true
		err = scanHistory(file, runs)
		closeErr := file.Close()
		if err != nil {
			return nil, fmt.Errorf("read run history %s: %w", candidate, err)
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if !readAny {
		return nil, nil
	}

	result := make([]RunSummary, 0, len(runs))
	for _, summary := range runs {
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedAt.After(result[j].StartedAt)
	})
	return result, nil
}

func FindRun(runs []RunSummary, id string) (RunSummary, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return RunSummary{}, errors.New("run ID must not be empty")
	}
	var matches []RunSummary
	for _, run := range runs {
		if run.RunID == id || strings.HasPrefix(strings.TrimPrefix(run.RunID, "run_"), strings.TrimPrefix(id, "run_")) {
			matches = append(matches, run)
		}
	}
	switch len(matches) {
	case 0:
		return RunSummary{}, fmt.Errorf("run %q not found", id)
	case 1:
		return matches[0], nil
	default:
		return RunSummary{}, fmt.Errorf("run ID prefix %q is ambiguous", id)
	}
}

func scanHistory(file *os.File, runs map[string]RunSummary) error {
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		data := scanner.Bytes()
		if len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		event, err := decodeHistoryEvent(data)
		if err != nil {
			continue
		}
		applyEvent(runs, event)
	}
	return scanner.Err()
}

func decodeHistoryEvent(data []byte) (Event, error) {
	var version struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return Event{}, err
	}
	if version.SchemaVersion != "" {
		var event Event
		if err := json.Unmarshal(data, &event); err != nil {
			return Event{}, err
		}
		if event.SchemaVersion != SchemaVersion {
			return Event{}, fmt.Errorf("unsupported telemetry schema %q", event.SchemaVersion)
		}
		return event, nil
	}
	return decodeLegacyEvent(data)
}

type legacyEvent struct {
	Timestamp  time.Time         `json:"timestamp"`
	RunID      string            `json:"run_id"`
	SessionID  string            `json:"session_id"`
	EventType  string            `json:"event_type"`
	AgentName  string            `json:"agent_name"`
	Repo       string            `json:"repo"`
	Model      string            `json:"model"`
	Attempt    int               `json:"attempt"`
	Outcome    string            `json:"outcome"`
	ExitCode   *int              `json:"exit_code"`
	DurationMS int64             `json:"duration_ms"`
	Usage      map[string]string `json:"usage"`
	Metadata   map[string]string `json:"metadata"`
}

func decodeLegacyEvent(data []byte) (Event, error) {
	var legacy legacyEvent
	if err := json.Unmarshal(data, &legacy); err != nil {
		return Event{}, err
	}
	if legacy.RunID == "" || legacy.EventType == "" {
		return Event{}, errors.New("missing run_id or event_type")
	}
	agent, model := ResolveAgentModel(legacy.AgentName, []string{legacy.AgentName})
	if legacy.Model != "" && legacy.Model != "unknown" {
		model.Requested = legacy.Model
		model.Provider = firstNonEmpty(providerForModel(legacy.Model), model.Provider)
		model.Family = firstNonEmpty(familyForModel(legacy.Model), model.Family)
		model.Resolution = ModelResolution{Status: "resolved", Confidence: "configured", PrimarySource: "legacy", Sources: []string{"legacy"}}
	}
	return Event{
		SchemaVersion: SchemaVersion,
		Timestamp:     legacy.Timestamp,
		EventType:     legacy.EventType,
		RunID:         legacy.RunID,
		TraceID:       traceIDForRun(legacy.RunID),
		Repository:    RepositoryMetadata{Slug: legacy.Repo},
		Agent:         agent,
		Model:         model,
		SessionID:     legacy.SessionID,
		Attempt:       legacy.Attempt,
		Outcome:       legacy.Outcome,
		ExitCode:      legacy.ExitCode,
		DurationMS:    legacy.DurationMS,
		Metadata:      legacy.Metadata,
	}, nil
}

func applyEvent(runs map[string]RunSummary, event Event) {
	summary, exists := runs[event.RunID]
	if !exists {
		summary = RunSummary{
			SchemaVersion: event.SchemaVersion,
			RunID:         event.RunID,
			TraceID:       event.TraceID,
			StartedAt:     event.Timestamp,
			Mode:          runMode(event.Task.Ref),
			Task:          event.Task,
			Repository:    event.Repository,
			Agent:         event.Agent,
			Model:         event.Model,
			Execution: ExecutionSummary{
				VerifyEnabled: event.VerifyEnabled,
				MaxRetries:    event.MaxRetries,
			},
			Verification: VerificationSummary{Outcome: "not_run"},
			Runtime:      event.Runtime,
		}
	}
	summary.Repository = event.Repository
	summary.Agent = event.Agent
	summary.Model = event.Model
	summary.Task = event.Task
	summary.Usage = cloneUsage(event.Usage)
	summary.Runtime = event.Runtime
	summary.Diagnostics = event.Diagnostics
	if event.SessionID != "" {
		summary.Broker.SessionID = event.SessionID
	}
	switch event.EventType {
	case "run.started":
		summary.StartedAt = event.Timestamp
	case "session.created":
		summary.Broker.SessionCreated = true
	case "session.revoked":
		summary.Broker.SessionRevoked = true
	case "agent.command.started", "agent.command.finished":
		summary.Execution.AgentAttempts = max(summary.Execution.AgentAttempts, event.Attempt)
	case "verify.command.started", "verify.attempt.started":
		summary.Execution.VerifyAttempts = max(summary.Execution.VerifyAttempts, event.Attempt)
		if hash := event.Metadata["command_sha256"]; hash != "" {
			summary.Verification.CommandSHA256 = hash
		}
	case "verify.command.finished", "verify.attempt.finished":
		summary.Execution.VerifyAttempts = max(summary.Execution.VerifyAttempts, event.Attempt)
		summary.Verification.Outcome = event.Outcome
		summary.Verification.LastExitCode = cloneInt(event.ExitCode)
	case "run.finished":
		ended := event.Timestamp
		summary.EndedAt = &ended
		summary.DurationMS = event.DurationMS
		summary.Outcome = event.Outcome
		summary.TerminalPhase = event.Phase
		summary.ExitCode = cloneInt(event.ExitCode)
		summary.Signal = event.Signal
	}
	runs[event.RunID] = summary
}
