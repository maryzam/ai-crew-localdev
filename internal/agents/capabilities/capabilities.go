package capabilities

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

type Entry struct {
	Name             string
	Type             string
	Provider         string
	ModelFamily      string
	Tools            []string
	ModelEnv         []string
	NativeTelemetry  NativeTelemetry
	Login            Login
	ProjectedPaths   []ProjectedPath
	GuidanceTargets  []GuidanceTarget
	DefaultToolNames []string
}

type NativeTelemetry struct {
	Supported bool
	Surface   NativeTelemetrySurface
}

type NativeTelemetrySurface string

const (
	NativeTelemetryNone    NativeTelemetrySurface = ""
	NativeTelemetryEnv     NativeTelemetrySurface = "env"
	NativeTelemetryCommand NativeTelemetrySurface = "command"
)

type Login struct {
	Probe       []string
	InstallHint string
	Remediation string
}

type AgentAttribution struct {
	Type    string
	Command string
}

type ModelAttribution struct {
	Provider   string
	Family     string
	Requested  string
	Resolution ModelResolution
}

type ModelResolution struct {
	Status        string
	Confidence    string
	PrimarySource string
	Sources       []string
	Conflict      bool
}

type modelSignal struct {
	model      string
	source     string
	confidence string
}

type ProjectedPathKind string

const (
	ProjectedPathDir  ProjectedPathKind = "dir"
	ProjectedPathFile ProjectedPathKind = "file"
)

type ProjectedPath struct {
	Name    string
	Kind    ProjectedPathKind
	Exclude []string
}

type GuidanceTarget struct {
	Asset string
	Path  string
}

const (
	GlobalGuidanceAsset = "assets/global-guidance.md"
	AuditSkillAsset     = "assets/skills/token-efficiency-audit/SKILL.md"
)

func Registry() []Entry {
	return []Entry{
		{
			Name:        "claude",
			Type:        "claude_code",
			Provider:    "anthropic",
			ModelFamily: "claude",
			Tools:       []string{"claude", "claude-code"},
			ModelEnv:    []string{"CLAUDE_MODEL", "ANTHROPIC_MODEL"},
			NativeTelemetry: NativeTelemetry{
				Supported: true,
				Surface:   NativeTelemetryEnv,
			},
			Login: Login{
				Probe:       []string{"auth", "status", "--json"},
				InstallHint: "install the Claude Code CLI (npm install -g @anthropic-ai/claude-code) or use the generic devcontainer where it is preinstalled",
				Remediation: "run 'claude' or 'claude auth login' once inside this devcontainer; login persists in /home/dev across container replacement",
			},
			ProjectedPaths: []ProjectedPath{
				{Name: ".claude", Kind: ProjectedPathDir},
				{Name: ".claude.json", Kind: ProjectedPathFile},
			},
			GuidanceTargets: []GuidanceTarget{
				{Asset: GlobalGuidanceAsset, Path: filepath.Join(".claude", "CLAUDE.md")},
				{Asset: AuditSkillAsset, Path: filepath.Join(".claude", "skills", "token-efficiency-audit", "SKILL.md")},
			},
			DefaultToolNames: []string{"claude-code"},
		},
		{
			Name:        "codex",
			Type:        "codex",
			Provider:    "openai",
			ModelFamily: "openai-codex",
			Tools:       []string{"codex"},
			ModelEnv:    []string{"CODEX_MODEL", "OPENAI_MODEL"},
			NativeTelemetry: NativeTelemetry{
				Supported: true,
				Surface:   NativeTelemetryCommand,
			},
			Login: Login{
				Probe:       []string{"login", "status"},
				InstallHint: "install the Codex CLI (npm install -g @openai/codex) or use the generic devcontainer where it is preinstalled",
				Remediation: "run 'codex login' (or 'codex login --with-api-key') once inside this devcontainer; login persists in /home/dev across container replacement",
			},
			ProjectedPaths: []ProjectedPath{
				{Name: ".codex", Kind: ProjectedPathDir, Exclude: []string{"packages", "tmp"}},
				{Name: ".agents", Kind: ProjectedPathDir},
			},
			GuidanceTargets: []GuidanceTarget{
				{Asset: GlobalGuidanceAsset, Path: filepath.Join(".codex", "AGENTS.md")},
				{Asset: AuditSkillAsset, Path: filepath.Join(".agents", "skills", "token-efficiency-audit", "SKILL.md")},
			},
			DefaultToolNames: []string{"codex"},
		},
	}
}

func Find(name string) (Entry, bool) {
	normalized := normalize(name)
	for _, entry := range Registry() {
		if entry.matches(normalized) {
			return cloneEntry(entry), true
		}
	}
	return Entry{}, false
}

func FindByCommand(command string) (Entry, bool) {
	name := normalize(command)
	for _, entry := range Registry() {
		for _, tool := range entry.Tools {
			if normalize(tool) == name {
				return cloneEntry(entry), true
			}
		}
	}
	return Entry{}, false
}

func CommandMatchesTool(commandName string, tool string) bool {
	commandName = normalize(commandName)
	tool = normalize(tool)
	for _, entry := range Registry() {
		if entry.hasTool(tool) {
			return entry.hasTool(commandName)
		}
	}
	return commandName != "" && commandName == tool
}

func ResolveAttribution(agentName, configuredModel string, command []string) (AgentAttribution, ModelAttribution) {
	agentType := InferType(agentName, command)
	agent := AgentAttribution{Type: agentType, Command: safeCommandName(command)}
	model := ModelAttribution{
		Provider: providerForAgent(agentType),
		Family:   familyForAgent(agentType),
		Resolution: ModelResolution{
			Status:        "partial",
			Confidence:    "inferred",
			PrimarySource: "agent_type",
			Sources:       []string{"agent_type"},
		},
	}
	signals := configuredModelSignals(agentType, configuredModel, command)
	if len(signals) == 0 {
		if model.Provider == "" && model.Family == "" {
			model.Resolution = ModelResolution{Status: "unresolved", Confidence: "unresolved", PrimarySource: "none"}
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
		if !contains(model.Resolution.Sources, signal.source) {
			model.Resolution.Sources = append(model.Resolution.Sources, signal.source)
		}
		if !strings.EqualFold(signal.model, primary.model) {
			model.Resolution.Conflict = true
		}
	}
	return agent, model
}

func InferType(agentName string, command []string) string {
	if entry, ok := Find(agentName); ok {
		return entry.Type
	}
	normalizedAgent := normalize(agentName)
	for _, entry := range Registry() {
		if strings.Contains(normalizedAgent, normalize(entry.Name)) || strings.Contains(normalizedAgent, normalize(entry.Type)) {
			return entry.Type
		}
	}
	if len(command) > 0 {
		if entry, ok := FindByCommand(command[0]); ok {
			return entry.Type
		}
	}
	return "other"
}

func configuredModelSignals(agentType, configuredModel string, command []string) []modelSignal {
	var signals []modelSignal
	if model := modelFromArgs(command); model != "" {
		signals = append(signals, modelSignal{model: model, source: "cli", confidence: "configured"})
	}
	for _, key := range modelEnvKeys(agentType) {
		if model := strings.TrimSpace(os.Getenv(key)); model != "" {
			signals = append(signals, modelSignal{model: bounded(model, 200), source: "environment", confidence: "configured"})
		}
	}
	if model := strings.TrimSpace(configuredModel); model != "" {
		signals = append(signals, modelSignal{model: bounded(model, 200), source: "identity_config", confidence: "configured"})
	}
	return signals
}

func modelFromArgs(command []string) string {
	for i, arg := range command {
		if (arg == "--model" || arg == "-m") && i+1 < len(command) {
			return bounded(command[i+1], 200)
		}
		if value, ok := strings.CutPrefix(arg, "--model="); ok {
			return bounded(value, 200)
		}
	}
	return ""
}

func modelEnvKeys(agentType string) []string {
	keys := []string{paths.EnvModel}
	if entry, ok := Find(agentType); ok {
		return append(keys, entry.ModelEnv...)
	}
	keys = append(keys, "OPENAI_MODEL", "ANTHROPIC_MODEL", "GEMINI_MODEL")
	return keys
}

func NativeTelemetryForCommand(command []string) (NativeTelemetry, bool) {
	if len(command) == 0 {
		return NativeTelemetry{}, false
	}
	entry, ok := FindByCommand(command[0])
	if !ok || !entry.NativeTelemetry.Supported {
		return NativeTelemetry{}, false
	}
	return entry.NativeTelemetry, true
}

func ProjectedHomePaths() []ProjectedPath {
	var paths []ProjectedPath
	for _, entry := range Registry() {
		paths = append(paths, entry.ProjectedPaths...)
	}
	return cloneProjectedPaths(paths)
}

func GuidanceTargets() []GuidanceTarget {
	var targets []GuidanceTarget
	for _, entry := range Registry() {
		targets = append(targets, entry.GuidanceTargets...)
	}
	return cloneGuidanceTargets(targets)
}

func DefaultToolForAgent(agentName string) string {
	entry, ok := Find(agentName)
	if !ok || len(entry.DefaultToolNames) == 0 {
		return ""
	}
	return entry.DefaultToolNames[0]
}

func (entry Entry) matches(value string) bool {
	if value == normalize(entry.Name) || value == normalize(entry.Type) {
		return true
	}
	return entry.hasTool(value)
}

func (entry Entry) hasTool(value string) bool {
	for _, tool := range entry.Tools {
		if normalize(tool) == value {
			return true
		}
	}
	return false
}

func normalize(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".exe")
	return strings.ToLower(value)
}

func cloneEntry(entry Entry) Entry {
	entry.Tools = append([]string(nil), entry.Tools...)
	entry.ModelEnv = append([]string(nil), entry.ModelEnv...)
	entry.Login.Probe = append([]string(nil), entry.Login.Probe...)
	entry.ProjectedPaths = cloneProjectedPaths(entry.ProjectedPaths)
	entry.GuidanceTargets = cloneGuidanceTargets(entry.GuidanceTargets)
	entry.DefaultToolNames = append([]string(nil), entry.DefaultToolNames...)
	return entry
}

func cloneProjectedPaths(paths []ProjectedPath) []ProjectedPath {
	clone := make([]ProjectedPath, len(paths))
	for i, path := range paths {
		clone[i] = path
		clone[i].Exclude = append([]string(nil), path.Exclude...)
	}
	return clone
}

func cloneGuidanceTargets(targets []GuidanceTarget) []GuidanceTarget {
	return append([]GuidanceTarget(nil), targets...)
}

func safeCommandName(command []string) string {
	if len(command) == 0 {
		return ""
	}
	return normalize(command[0])
}

func providerForAgent(agentType string) string {
	if entry, ok := Find(agentType); ok {
		return entry.Provider
	}
	return ""
}

func familyForAgent(agentType string) string {
	if entry, ok := Find(agentType); ok {
		return entry.ModelFamily
	}
	return ""
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

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func bounded(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	return value[:maxLength]
}
