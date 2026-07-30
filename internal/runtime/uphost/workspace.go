package uphost

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func PrepareWorkspace(workspacePath, projectPath string) (string, error) {
	target := workspacePath
	if projectPath != "" {
		target = projectPath
	}
	workspace, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if err := os.Setenv(paths.EnvWorkspace, workspace); err != nil {
		return "", fmt.Errorf("set workspace environment: %w", err)
	}
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		if err := os.Setenv("XDG_RUNTIME_DIR", paths.RuntimeBaseDir()); err != nil {
			return "", fmt.Errorf("set runtime environment: %w", err)
		}
	}
	if err := os.MkdirAll(paths.RuntimeDir(), 0o700); err != nil {
		return "", fmt.Errorf("create runtime dir %s: %w", paths.RuntimeDir(), err)
	}
	return workspace, nil
}

func SoleRepository(workspace string) (string, bool) {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return "", false
	}
	found := ""
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(workspace, entry.Name(), ".git")); err != nil {
			continue
		}
		if found != "" {
			return "", false
		}
		found = entry.Name()
	}
	return found, found != ""
}
