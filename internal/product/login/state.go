package login

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
	"github.com/maryzam/ai-crew-localdev/internal/product/securefile"
)

type Paths struct {
	Root      string
	LoginRoot string
	AgentRoot string
	BinaryDir string
	Binary    string
	Evidence  string
}

func ResolvePaths(agent agents.Agent) (Paths, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve home directory: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	if !filepath.IsAbs(dataHome) {
		return Paths{}, errors.New("XDG_DATA_HOME must be absolute")
	}
	root := filepath.Join(dataHome, "ai-crew")
	loginRoot := filepath.Join(root, "broker", "login")
	paths := Paths{Root: root, LoginRoot: loginRoot, AgentRoot: filepath.Join(loginRoot, agent.Name), Evidence: filepath.Join(root, "evidence")}
	if agent.Refresh != nil {
		paths.BinaryDir = filepath.Join(root, "broker", "bin", agent.Name+"-"+agent.Version)
		paths.Binary = filepath.Join(paths.BinaryDir, agent.Refresh.FileName)
	}
	return paths, nil
}

func (p Paths) privateDirectories() []string {
	directories := []string{p.Root, filepath.Dir(p.LoginRoot), p.LoginRoot, p.AgentRoot}
	if p.BinaryDir != "" {
		directories = append(directories, filepath.Dir(p.BinaryDir), p.BinaryDir)
	}
	return directories
}

func preparePaths(paths Paths) error {
	for _, path := range paths.privateDirectories() {
		if err := securefile.EnsureOwnerOnlyDir(path); err != nil {
			return err
		}
	}
	return nil
}
