package devcontainer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/platform/embedasset"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/assetsource"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/devcontainer/assets"
)

const (
	rootDirName      = "devcontainer"
	configDirName    = ".devcontainer"
	binaryDirName    = "bin"
	binaryTargetName = "ai-agent"
)

func canonicalWorkspace(workspace string) string {
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		return resolved
	}
	return filepath.Clean(workspace)
}

func workspaceKey(workspace string) string {
	sum := sha256.Sum256([]byte(canonicalWorkspace(workspace)))
	return hex.EncodeToString(sum[:])[:16]
}

func GenericRootPath(dataDir, workspace string) string {
	return filepath.Join(dataDir, rootDirName, workspaceKey(workspace))
}

func PrepareGenericRoot(dataDir, workspace string, executable func() (string, error)) (string, error) {
	if executable == nil {
		return "", fmt.Errorf("resolve ai-agent binary: no executable resolver")
	}
	self, err := executable()
	if err != nil {
		return "", fmt.Errorf("resolve ai-agent binary: %w", err)
	}
	root := GenericRootPath(dataDir, workspace)
	if err := writeGenericConfig(root); err != nil {
		return "", err
	}
	if err := installBinary(self, filepath.Join(root, binaryDirName, binaryTargetName)); err != nil {
		return "", err
	}
	return root, nil
}

func writeGenericConfig(root string) error {
	generic, err := assets.Generic()
	if err != nil {
		return fmt.Errorf("load devcontainer assets: %w", err)
	}
	source := assetsource.FS(generic, configDirName)
	return embedasset.Materialize(source, filepath.Join(root, configDirName), assets.Mode)
}

func installBinary(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(target), err)
	}
	binary, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("read ai-agent binary %s: %w", source, err)
	}
	defer func() { _ = binary.Close() }()

	return stageAndRename(target, assets.ExecutableMode, func(staged *os.File) error {
		_, copyErr := io.Copy(staged, binary)
		return copyErr
	})
}

func stageAndRename(target string, mode fs.FileMode, write func(*os.File) error) error {
	staged, err := os.CreateTemp(filepath.Dir(target), ".ai-agent-*")
	if err != nil {
		return fmt.Errorf("stage %s: %w", target, err)
	}
	stagedPath := staged.Name()
	defer func() { _ = os.Remove(stagedPath) }()

	if err := write(staged); err != nil {
		_ = staged.Close()
		return fmt.Errorf("write %s: %w", target, err)
	}
	if err := staged.Chmod(mode); err != nil {
		_ = staged.Close()
		return fmt.Errorf("set mode on %s: %w", target, err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("stage %s: %w", target, err)
	}
	if err := os.Rename(stagedPath, target); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}
