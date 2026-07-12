package agentdefaults

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	agentcaps "github.com/maryzam/ai-crew-localdev/internal/agents/capabilities"
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

	targets := agentcaps.GuidanceTargets()
	destinations := make([]destination, 0, len(targets))
	for _, target := range targets {
		destinations = append(destinations, destination{asset: target.Asset, path: filepath.Join(home, target.Path)})
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
