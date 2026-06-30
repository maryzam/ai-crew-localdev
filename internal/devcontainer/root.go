package devcontainer

import (
	"fmt"
	"os"
	"path/filepath"
)

func FindRoot(executable func() (string, error), workingDir func() (string, error)) (string, error) {
	if executable != nil {
		if self, err := executable(); err == nil {
			if root, found := walkUp(filepath.Dir(self)); found {
				return root, nil
			}
		}
	}
	if workingDir != nil {
		if cwd, err := workingDir(); err == nil {
			if root, found := walkUp(cwd); found {
				return root, nil
			}
		}
	}
	return "", fmt.Errorf(".devcontainer/ not found; run from the ai-crew-localdev checkout or ensure the binary is co-located with the project")
}

func walkUp(dir string) (string, bool) {
	current := dir
	for {
		if _, err := os.Stat(filepath.Join(current, ".devcontainer")); err == nil {
			return current, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}
