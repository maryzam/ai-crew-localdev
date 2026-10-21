package login

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
)

type Paths struct {
	Root       string
	LoginRoot  string
	AgentRoot  string
	Credential string
	BinaryRoot string
	Binary     string
	Audit      string
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
		return Paths{}, fmt.Errorf("XDG_DATA_HOME must be absolute")
	}
	root := filepath.Join(dataHome, "ai-crew")
	loginRoot := filepath.Join(root, "broker", "login")
	agentRoot := filepath.Join(loginRoot, agent.Name)
	binaryRoot := filepath.Join(root, "broker", "bin")
	return Paths{Root: root, LoginRoot: loginRoot, AgentRoot: agentRoot, Credential: filepath.Join(agentRoot, agent.CredentialFile), BinaryRoot: binaryRoot, Binary: filepath.Join(binaryRoot, "codex-"+agent.Version), Audit: filepath.Join(loginRoot, "audit.jsonl")}, nil
}

func preparePaths(paths Paths) error {
	for _, path := range []string{paths.Root, filepath.Dir(paths.LoginRoot), paths.LoginRoot, paths.AgentRoot, paths.BinaryRoot} {
		if err := ensurePrivateDirectory(path); err != nil {
			return err
		}
	}
	return nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("prepare private directory parent: %w", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create private directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("private directory must be an owner-only directory: %s", path)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("private directory must be owned by the current user: %s", path)
	}
	return nil
}
