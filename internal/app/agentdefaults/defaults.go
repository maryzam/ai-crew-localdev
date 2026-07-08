package agentdefaults

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/agentstate"
)

const (
	globalGuidanceAsset = "assets/global-guidance.md"
	auditSkillAsset     = "assets/skills/token-efficiency-audit/SKILL.md"
)

//go:embed assets/global-guidance.md assets/skills/token-efficiency-audit/SKILL.md
var assets embed.FS

type Result struct {
	Installed []string
	Skipped   []string
}

type destination struct {
	asset string
	path  string
}

func Install(home string) (Result, error) {
	if home == "" || !filepath.IsAbs(home) {
		return Result{}, fmt.Errorf("agent defaults: home must be an absolute path")
	}

	destinations := []destination{
		{asset: globalGuidanceAsset, path: filepath.Join(home, agentstate.CodexDir, "AGENTS.md")},
		{asset: globalGuidanceAsset, path: filepath.Join(home, agentstate.ClaudeDir, "CLAUDE.md")},
		{asset: auditSkillAsset, path: filepath.Join(home, agentstate.AgentsDir, "skills", "token-efficiency-audit", "SKILL.md")},
		{asset: auditSkillAsset, path: filepath.Join(home, agentstate.ClaudeDir, "skills", "token-efficiency-audit", "SKILL.md")},
	}

	var result Result
	for _, item := range destinations {
		installed, err := installExclusive(item.asset, item.path)
		if err != nil {
			return result, err
		}
		if installed {
			result.Installed = append(result.Installed, item.path)
		} else {
			result.Skipped = append(result.Skipped, item.path)
		}
	}
	return result, nil
}

func installExclusive(asset, destination string) (bool, error) {
	data, err := fs.ReadFile(assets, asset)
	if err != nil {
		return false, fmt.Errorf("agent defaults: read embedded %s: %w", asset, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return false, fmt.Errorf("agent defaults: create %s: %w", filepath.Dir(destination), err)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("agent defaults: create %s: %w", destination, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(destination)
		return false, fmt.Errorf("agent defaults: write %s: %w", destination, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(destination)
		return false, fmt.Errorf("agent defaults: close %s: %w", destination, err)
	}
	return true, nil
}
