package modelattrib

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

const MaxPropagatedValueLength = 200

type AgentProfile struct {
	Name     string
	Type     string
	Provider string
	Family   string
	Tools    []string
	ModelEnv []string
}

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

func ClaudeProfile() AgentProfile {
	return cloneProfile(AgentProfile{
		Name:     "claude",
		Type:     "claude_code",
		Provider: "anthropic",
		Family:   "claude",
		Tools:    []string{"claude", "claude-code"},
		ModelEnv: []string{"CLAUDE_MODEL", "ANTHROPIC_MODEL"},
	})
}

func CodexProfile() AgentProfile {
	return cloneProfile(AgentProfile{
		Name:     "codex",
		Type:     "codex",
		Provider: "openai",
		Family:   "openai-codex",
		Tools:    []string{"codex"},
		ModelEnv: []string{"CODEX_MODEL", "OPENAI_MODEL"},
	})
}

func GeminiProfile() AgentProfile {
	return cloneProfile(AgentProfile{
		Name:     "gemini",
		Type:     "gemini",
		Provider: "gcp.gemini",
		Family:   "gemini",
		Tools:    []string{"gemini"},
		ModelEnv: []string{"GEMINI_MODEL"},
	})
}

func StandardProfiles() []AgentProfile {
	return cloneProfiles([]AgentProfile{ClaudeProfile(), CodexProfile(), GeminiProfile()})
}

func Resolve(agentName, configuredModel string, command []string, profiles []AgentProfile) (AgentMetadata, ModelAttribution) {
	profiles = completeProfiles(profiles)
	agentType := InferType(agentName, command, profiles)
	agent := AgentMetadata{Type: agentType, Identity: agentName, Command: CommandName(command)}
	model := ModelAttribution{
		Provider: providerForAgent(agentType, profiles),
		Family:   familyForAgent(agentType, profiles),
		Resolution: ModelResolution{
			Status:        "partial",
			Confidence:    "inferred",
			PrimarySource: "agent_type",
			Sources:       []string{"agent_type"},
		},
	}
	signals := configuredModelSignals(agentType, configuredModel, command, profiles)
	if len(signals) == 0 {
		if model.Provider == "" && model.Family == "" {
			model.Resolution = ModelResolution{Status: "unresolved", Confidence: "unresolved", PrimarySource: "none"}
		}
		return agent, model
	}
	primary := signals[0]
	model.Requested = primary.model
	model.Provider = FirstNonEmpty(ProviderForModel(primary.model), model.Provider)
	model.Family = FirstNonEmpty(FamilyForModel(primary.model), model.Family)
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

func InferType(agentName string, command []string, profiles []AgentProfile) string {
	profiles = completeProfiles(profiles)
	values := []string{normalize(agentName)}
	if len(command) > 0 {
		values = append(values, normalize(command[0]))
	}
	for _, value := range values {
		for _, profile := range profiles {
			if matchesProfile(value, profile) {
				return profile.Type
			}
		}
	}
	return "other"
}

func CommandName(command []string) string {
	if len(command) == 0 {
		return ""
	}
	return filepath.Base(strings.TrimSpace(command[0]))
}

func ProviderForModel(model string) string {
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

func FamilyForModel(model string) string {
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

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func Bounded(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	return value[:maxLength]
}

func configuredModelSignals(agentType, configuredModel string, command []string, profiles []AgentProfile) []modelSignal {
	var signals []modelSignal
	if model := modelFromArgs(command); model != "" {
		signals = append(signals, modelSignal{model: model, source: "cli", confidence: "configured"})
	}
	for _, key := range modelEnvKeys(agentType, profiles) {
		if model := strings.TrimSpace(os.Getenv(key)); model != "" {
			signals = append(signals, modelSignal{model: Bounded(model, MaxPropagatedValueLength), source: "environment", confidence: "configured"})
		}
	}
	if model := strings.TrimSpace(configuredModel); model != "" {
		signals = append(signals, modelSignal{model: Bounded(model, MaxPropagatedValueLength), source: "identity_config", confidence: "configured"})
	}
	return signals
}

func modelFromArgs(command []string) string {
	for i, arg := range command {
		if (arg == "--model" || arg == "-m") && i+1 < len(command) {
			return Bounded(command[i+1], MaxPropagatedValueLength)
		}
		if value, ok := strings.CutPrefix(arg, "--model="); ok {
			return Bounded(value, MaxPropagatedValueLength)
		}
	}
	return ""
}

func modelEnvKeys(agentType string, profiles []AgentProfile) []string {
	keys := []string{paths.EnvModel}
	if profile, ok := findProfile(agentType, profiles); ok {
		return append(keys, profile.ModelEnv...)
	}
	keys = append(keys, "OPENAI_MODEL", "ANTHROPIC_MODEL", "GEMINI_MODEL")
	return keys
}

func providerForAgent(agentType string, profiles []AgentProfile) string {
	if profile, ok := findProfile(agentType, profiles); ok {
		return profile.Provider
	}
	return ""
}

func familyForAgent(agentType string, profiles []AgentProfile) string {
	if profile, ok := findProfile(agentType, profiles); ok {
		return profile.Family
	}
	return ""
}

func findProfile(value string, profiles []AgentProfile) (AgentProfile, bool) {
	normalized := normalize(value)
	for _, profile := range completeProfiles(profiles) {
		if normalized == normalize(profile.Name) || normalized == normalize(profile.Type) {
			return cloneProfile(profile), true
		}
		for _, tool := range profile.Tools {
			if normalized == normalize(tool) {
				return cloneProfile(profile), true
			}
		}
	}
	return AgentProfile{}, false
}

func matchesProfile(value string, profile AgentProfile) bool {
	if value == "" {
		return false
	}
	candidates := append([]string{profile.Name, profile.Type}, profile.Tools...)
	for _, candidate := range candidates {
		normalized := normalize(candidate)
		if normalized != "" && (value == normalized || strings.Contains(value, normalized)) {
			return true
		}
	}
	return false
}

func completeProfiles(profiles []AgentProfile) []AgentProfile {
	result := cloneProfiles(profiles)
	for _, standard := range StandardProfiles() {
		if _, ok := findProfileIn(standard.Type, result); !ok {
			result = append(result, standard)
		}
	}
	return result
}

func findProfileIn(value string, profiles []AgentProfile) (AgentProfile, bool) {
	normalized := normalize(value)
	for _, profile := range profiles {
		if normalized == normalize(profile.Name) || normalized == normalize(profile.Type) {
			return cloneProfile(profile), true
		}
		for _, tool := range profile.Tools {
			if normalized == normalize(tool) {
				return cloneProfile(profile), true
			}
		}
	}
	return AgentProfile{}, false
}

func normalize(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".exe")
	return strings.ToLower(value)
}

func cloneProfiles(profiles []AgentProfile) []AgentProfile {
	result := make([]AgentProfile, len(profiles))
	for i, profile := range profiles {
		result[i] = cloneProfile(profile)
	}
	return result
}

func cloneProfile(profile AgentProfile) AgentProfile {
	profile.Tools = append([]string(nil), profile.Tools...)
	profile.ModelEnv = append([]string(nil), profile.ModelEnv...)
	return profile
}
