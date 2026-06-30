package devcontainer

import (
	"fmt"
	"os"
	"path/filepath"
)

type RootFinder struct {
	Executable func() (string, error)
	WorkingDir func() (string, error)
}

func NewRootFinder() RootFinder {
	return RootFinder{Executable: os.Executable, WorkingDir: os.Getwd}
}

func (f RootFinder) Find() (string, error) {
	if f.Executable != nil {
		if self, err := f.Executable(); err == nil {
			if root, found := WalkUp(filepath.Dir(self)); found {
				return root, nil
			}
		}
	}
	if f.WorkingDir != nil {
		if cwd, err := f.WorkingDir(); err == nil {
			if root, found := WalkUp(cwd); found {
				return root, nil
			}
		}
	}
	return "", fmt.Errorf(".devcontainer/ not found; run from the ai-crew-localdev checkout or ensure the binary is co-located with the project")
}

func WalkUp(dir string) (string, bool) {
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
