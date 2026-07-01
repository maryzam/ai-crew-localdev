package telemetry

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type AgentMetadata struct {
	Type     string `json:"type"`
	Identity string `json:"identity"`
	Version  string `json:"version,omitempty"`
	Command  string `json:"command"`
}

type ModelResolution struct {
	Status        string   `json:"status"`
	Confidence    string   `json:"confidence"`
	PrimarySource string   `json:"primary_source"`
	Sources       []string `json:"sources"`
	Conflict      bool     `json:"conflict"`
}

type ModelAttribution struct {
	Provider   string          `json:"provider,omitempty"`
	Family     string          `json:"family,omitempty"`
	Requested  string          `json:"requested,omitempty"`
	Observed   string          `json:"observed,omitempty"`
	Resolution ModelResolution `json:"resolution"`
}

type modelSignal struct {
	model      string
	source     string
	confidence string
}

func ResolveAgentModelWithConfig(agentName, configuredModel string, command []string) (AgentMetadata, ModelAttribution) {
	agentType := inferAgentType(agentName, command)
	agent := AgentMetadata{
		Type:     boundedField("ai_agent.agent.type", agentType),
		Identity: boundedField("ai_agent.agent.identity", agentName),
		Command:  safeCommandName(command),
	}

	signals := configuredModelSignals(agentType, configuredModel, command)
	model := ModelAttribution{
		Provider: boundedField("gen_ai.provider.name", providerForAgent(agentType)),
		Family:   boundedField("ai_agent.model.family", familyForAgent(agentType)),
		Resolution: ModelResolution{
			Status:        "partial",
			Confidence:    "inferred",
			PrimarySource: "agent_type",
			Sources:       []string{"agent_type"},
		},
	}
	if len(signals) == 0 {
		if model.Provider == "" && model.Family == "" {
			model.Resolution.Status = "unresolved"
			model.Resolution.Confidence = "unresolved"
			model.Resolution.PrimarySource = "none"
			model.Resolution.Sources = nil
		}
		return agent, model
	}

	primary := signals[0]
	model.Requested = primary.model
	model.Provider = firstNonEmpty(providerForModel(primary.model), model.Provider)
	model.Family = firstNonEmpty(familyForModel(primary.model), model.Family)
	model.Resolution.Status = "resolved"
	model.Resolution.Confidence = primary.confidence
	model.Resolution.PrimarySource = primary.source
	model.Resolution.Sources = make([]string, 0, len(signals)+1)
	for _, signal := range signals {
		if !slices.Contains(model.Resolution.Sources, signal.source) {
			model.Resolution.Sources = append(model.Resolution.Sources, signal.source)
		}
		if !strings.EqualFold(signal.model, primary.model) {
			model.Resolution.Conflict = true
		}
	}
	return agent, model
}

func configuredModelSignals(agentType, configuredModel string, command []string) []modelSignal {
	var signals []modelSignal
	if model := modelFromArgs(command); model != "" {
		signals = append(signals, modelSignal{model: model, source: "cli", confidence: "configured"})
	}
	for _, key := range modelEnvKeys(agentType) {
		if model := strings.TrimSpace(os.Getenv(key)); model != "" {
			signals = append(signals, modelSignal{model: model, source: "environment", confidence: "configured"})
		}
	}
	if model := strings.TrimSpace(configuredModel); model != "" {
		signals = append(signals, modelSignal{model: bounded(model, MaxPropagatedValueLength), source: "identity_config", confidence: "configured"})
	}
	return signals
}

func modelFromArgs(command []string) string {
	for i, arg := range command {
		if (arg == "--model" || arg == "-m") && i+1 < len(command) {
			return bounded(command[i+1], MaxPropagatedValueLength)
		}
		if value, ok := strings.CutPrefix(arg, "--model="); ok {
			return bounded(value, MaxPropagatedValueLength)
		}
	}
	return ""
}

func modelEnvKeys(agentType string) []string {
	keys := []string{"AI_AGENT_MODEL"}
	switch agentType {
	case "codex":
		keys = append(keys, "CODEX_MODEL", "OPENAI_MODEL")
	case "claude_code":
		keys = append(keys, "CLAUDE_MODEL", "ANTHROPIC_MODEL")
	case "gemini":
		keys = append(keys, "GEMINI_MODEL")
	default:
		keys = append(keys, "OPENAI_MODEL", "ANTHROPIC_MODEL", "GEMINI_MODEL")
	}
	return keys
}

func inferAgentType(agentName string, command []string) string {
	values := []string{strings.ToLower(agentName)}
	if len(command) > 0 {
		values = append(values, strings.ToLower(filepath.Base(command[0])))
	}
	for _, value := range values {
		switch {
		case strings.Contains(value, "codex"):
			return "codex"
		case strings.Contains(value, "claude"):
			return "claude_code"
		case strings.Contains(value, "gemini"):
			return "gemini"
		}
	}
	return "other"
}

func providerForAgent(agentType string) string {
	switch agentType {
	case "codex":
		return "openai"
	case "claude_code":
		return "anthropic"
	case "gemini":
		return "gcp.gemini"
	default:
		return ""
	}
}

func familyForAgent(agentType string) string {
	switch agentType {
	case "codex":
		return "openai-codex"
	case "claude_code":
		return "claude"
	case "gemini":
		return "gemini"
	default:
		return ""
	}
}

func providerForModel(model string) string {
	model = strings.ToLower(model)
	switch {
	case strings.HasPrefix(model, "claude"):
		return "anthropic"
	case strings.HasPrefix(model, "gemini"):
		return "gcp.gemini"
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"), strings.HasPrefix(model, "o4"):
		return "openai"
	default:
		return ""
	}
}

func familyForModel(model string) string {
	value := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(value, "gpt-5"):
		return "gpt-5"
	case strings.HasPrefix(value, "gpt-4"):
		return "gpt-4"
	case strings.HasPrefix(value, "o1"):
		return "o1"
	case strings.HasPrefix(value, "o3"):
		return "o3"
	case strings.HasPrefix(value, "o4"):
		return "o4"
	case strings.HasPrefix(value, "claude") && strings.Contains(value, "opus"):
		return "claude-opus"
	case strings.HasPrefix(value, "claude") && strings.Contains(value, "sonnet"):
		return "claude-sonnet"
	case strings.HasPrefix(value, "claude") && strings.Contains(value, "haiku"):
		return "claude-haiku"
	case strings.HasPrefix(value, "claude"):
		return "claude"
	case strings.HasPrefix(value, "gemini-"):
		parts := strings.Split(value, "-")
		if len(parts) >= 2 {
			return strings.Join(parts[:2], "-")
		}
		return "gemini"
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
