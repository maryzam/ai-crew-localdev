package embedasset

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func Materialize(embedded fs.FS, destDir string, modeFor func(name string) fs.FileMode) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", destDir, err)
	}
	entries, err := fs.ReadDir(embedded, ".")
	if err != nil {
		return fmt.Errorf("read embedded assets: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := fs.ReadFile(embedded, entry.Name())
		if err != nil {
			return fmt.Errorf("read embedded asset %s: %w", entry.Name(), err)
		}
		if err := writeAtomic(filepath.Join(destDir, entry.Name()), content, modeFor(entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func Parity(embedded fs.FS, checkoutDir string) error {
	embeddedNames := make(map[string]bool)
	entries, err := fs.ReadDir(embedded, ".")
	if err != nil {
		return fmt.Errorf("read embedded assets: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		embeddedNames[name] = true
		embeddedContent, err := fs.ReadFile(embedded, name)
		if err != nil {
			return fmt.Errorf("read embedded asset %s: %w", name, err)
		}
		checkoutContent, err := os.ReadFile(filepath.Join(checkoutDir, name))
		if err != nil {
			return fmt.Errorf("%s is embedded but missing from %s: %w", name, checkoutDir, err)
		}
		if string(embeddedContent) != string(checkoutContent) {
			return fmt.Errorf("embedded %s drifted from %s", name, filepath.Join(checkoutDir, name))
		}
	}
	checkoutEntries, err := os.ReadDir(checkoutDir)
	if err != nil {
		return fmt.Errorf("read checkout %s: %w", checkoutDir, err)
	}
	for _, entry := range checkoutEntries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if !embeddedNames[entry.Name()] {
			return fmt.Errorf("%s is not embedded", filepath.Join(checkoutDir, entry.Name()))
		}
	}
	return nil
}

func writeAtomic(target string, content []byte, mode fs.FileMode) error {
	staged, err := os.CreateTemp(filepath.Dir(target), ".embedasset-*")
	if err != nil {
		return fmt.Errorf("stage %s: %w", target, err)
	}
	stagedPath := staged.Name()
	defer func() { _ = os.Remove(stagedPath) }()
	if _, err := staged.Write(content); err != nil {
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
