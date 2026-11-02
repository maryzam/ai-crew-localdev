package login

import (
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
	"github.com/maryzam/ai-crew-localdev/internal/product/paths"
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
	root, err := paths.Data()
	if err != nil {
		return Paths{}, err
	}
	evidenceRoot, err := paths.Evidence()
	if err != nil {
		return Paths{}, err
	}
	loginRoot := filepath.Join(root, "broker", "login")
	resolved := Paths{Root: root, LoginRoot: loginRoot, AgentRoot: filepath.Join(loginRoot, agent.Name), Evidence: evidenceRoot}
	if agent.Refresh != nil {
		resolved.BinaryDir = filepath.Join(root, "broker", "bin", agent.Name+"-"+agent.Version)
		resolved.Binary = filepath.Join(resolved.BinaryDir, agent.Refresh.FileName)
	}
	return resolved, nil
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
