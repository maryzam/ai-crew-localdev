package agentstate

import (
	"fmt"
	"path/filepath"
	"strings"
)

type Kind string

const (
	Dir  Kind = "dir"
	File Kind = "file"
)

const (
	ClaudeDir  = ".claude"
	ClaudeFile = ".claude.json"
	CodexDir   = ".codex"
	AgentsDir  = ".agents"
)

type Spec struct {
	Name    string
	Kind    Kind
	Exclude []string
}

var specs = []Spec{
	{Name: ClaudeDir, Kind: Dir},
	{Name: ClaudeFile, Kind: File},
	{Name: CodexDir, Kind: Dir, Exclude: []string{"packages", "tmp"}},
	{Name: AgentsDir, Kind: Dir},
}

var blockedNames = map[string]struct{}{
	".aws":             {},
	".azure":           {},
	".config":          {},
	".docker":          {},
	".git-credentials": {},
	".gitconfig":       {},
	".gnupg":           {},
	".kube":            {},
	".netrc":           {},
	".npmrc":           {},
	".pypirc":          {},
	".ssh":             {},
}

func Specs() []Spec {
	copied := make([]Spec, len(specs))
	for i, spec := range specs {
		copied[i] = spec
		copied[i].Exclude = append([]string(nil), spec.Exclude...)
	}
	return copied
}

func ValidateSpecs(values []Spec) error {
	seen := make(map[string]struct{}, len(values))
	for _, spec := range values {
		if err := validateSpec(spec); err != nil {
			return err
		}
		if _, ok := seen[spec.Name]; ok {
			return fmt.Errorf("duplicate agent state spec %q", spec.Name)
		}
		seen[spec.Name] = struct{}{}
	}
	return nil
}

func validateSpec(spec Spec) error {
	if spec.Name == "" || spec.Name == "." || spec.Name == ".." {
		return fmt.Errorf("invalid agent state name %q", spec.Name)
	}
	if filepath.IsAbs(spec.Name) || strings.Contains(spec.Name, "/") || strings.Contains(spec.Name, `\`) {
		return fmt.Errorf("agent state name %q must be one top-level path element", spec.Name)
	}
	if _, blocked := blockedNames[spec.Name]; blocked {
		return fmt.Errorf("agent state name %q is blocked", spec.Name)
	}
	switch spec.Kind {
	case Dir, File:
		for _, exclude := range spec.Exclude {
			if err := validateExclude(spec.Name, exclude); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("agent state %q has invalid kind %q", spec.Name, spec.Kind)
	}
}

func validateExclude(name string, exclude string) error {
	if exclude == "" || exclude == "." || exclude == ".." {
		return fmt.Errorf("agent state %q has invalid exclude %q", name, exclude)
	}
	if filepath.IsAbs(exclude) || strings.Contains(exclude, "/") || strings.Contains(exclude, `\`) {
		return fmt.Errorf("agent state %q exclude %q must be one path element", name, exclude)
	}
	return nil
}
