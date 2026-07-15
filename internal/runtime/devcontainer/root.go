package devcontainer

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/runtime/devcontainer/assets"
)

const (
	rootDirName      = "devcontainer"
	configDirName    = ".devcontainer"
	binaryDirName    = "bin"
	binaryTargetName = "ai-agent"
)

func GenericRootPath(dataDir string) string {
	return filepath.Join(dataDir, rootDirName)
}

func PrepareGenericRoot(dataDir string, executable func() (string, error)) (string, error) {
	if executable == nil {
		return "", fmt.Errorf("resolve ai-agent binary: no executable resolver")
	}
	self, err := executable()
	if err != nil {
		return "", fmt.Errorf("resolve ai-agent binary: %w", err)
	}
	root := GenericRootPath(dataDir)
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
	configDir := filepath.Join(root, configDirName)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", configDir, err)
	}
	entries, err := fs.ReadDir(generic, ".")
	if err != nil {
		return fmt.Errorf("read devcontainer assets: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := fs.ReadFile(generic, entry.Name())
		if err != nil {
			return fmt.Errorf("read devcontainer asset %s: %w", entry.Name(), err)
		}
		if err := writeFileAtomic(filepath.Join(configDir, entry.Name()), content, assets.Mode(entry.Name())); err != nil {
			return err
		}
	}
	return nil
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

func writeFileAtomic(target string, content []byte, mode fs.FileMode) error {
	return stageAndRename(target, mode, func(staged *os.File) error {
		_, writeErr := staged.Write(content)
		return writeErr
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
