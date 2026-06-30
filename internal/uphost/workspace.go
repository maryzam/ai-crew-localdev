package uphost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	upapplication "github.com/maryzam/ai-crew-localdev/internal/application/up"
	"github.com/maryzam/ai-crew-localdev/internal/config"
)

type Workspace struct{}

func (Workspace) Prepare(_ context.Context, input upapplication.Input) (string, error) {
	target := input.Workspace
	if input.Project != "" {
		target = input.Project
	}
	workspace, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if err := os.Setenv("AI_AGENT_WORKSPACE", workspace); err != nil {
		return "", fmt.Errorf("set workspace environment: %w", err)
	}
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		if err := os.Setenv("XDG_RUNTIME_DIR", config.RuntimeBaseDir()); err != nil {
			return "", fmt.Errorf("set runtime environment: %w", err)
		}
	}
	if err := os.MkdirAll(config.RuntimeDir(), 0o700); err != nil {
		return "", fmt.Errorf("create runtime dir %s: %w", config.RuntimeDir(), err)
	}
	return workspace, nil
}
