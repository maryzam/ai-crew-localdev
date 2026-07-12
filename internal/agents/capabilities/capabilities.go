package capabilities

import (
	"path/filepath"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/platform/modelattrib"
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

type AgentAttribution = modelattrib.AgentMetadata
type ModelAttribution = modelattrib.ModelAttribution
type ModelResolution = modelattrib.ModelResolution

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
	claude := modelattrib.ClaudeProfile()
	codex := modelattrib.CodexProfile()
	return []Entry{
		{
			Name:        claude.Name,
			Type:        claude.Type,
			Provider:    claude.Provider,
			ModelFamily: claude.Family,
			Tools:       claude.Tools,
			ModelEnv:    claude.ModelEnv,
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
			Name:        codex.Name,
			Type:        codex.Type,
			Provider:    codex.Provider,
			ModelFamily: codex.Family,
			Tools:       codex.Tools,
			ModelEnv:    codex.ModelEnv,
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
	return modelattrib.Resolve(agentName, configuredModel, command, attributionProfiles())
}

func InferType(agentName string, command []string) string {
	return modelattrib.InferType(agentName, command, attributionProfiles())
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

func attributionProfiles() []modelattrib.AgentProfile {
	entries := Registry()
	profiles := make([]modelattrib.AgentProfile, 0, len(entries))
	for _, entry := range entries {
		profiles = append(profiles, modelattrib.AgentProfile{
			Name:     entry.Name,
			Type:     entry.Type,
			Provider: entry.Provider,
			Family:   entry.ModelFamily,
			Tools:    append([]string(nil), entry.Tools...),
			ModelEnv: append([]string(nil), entry.ModelEnv...),
		})
	}
	return profiles
}
