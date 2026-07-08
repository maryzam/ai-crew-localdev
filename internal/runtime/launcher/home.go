package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var isolatedHomeDirs = []string{".claude", ".codex", ".agents"}

var isolatedHomeFiles = []string{".claude.json"}

var isolatedXDGVars = []string{
	"XDG_CONFIG_HOME",
	"XDG_DATA_HOME",
	"XDG_STATE_HOME",
	"XDG_CACHE_HOME",
}

func prepareIsolatedHome(realHome string) (dir string, cleanup func(), err error) {
	noop := func() {}
	dir, err = os.MkdirTemp("", "ai-agent-run-home-*")
	if err != nil {
		return "", noop, fmt.Errorf("create isolated home: %w", err)
	}
	remove := func() { _ = os.RemoveAll(dir) }

	if realHome == "" {
		return dir, remove, nil
	}
	for _, entry := range isolatedHomeDirs {
		target := filepath.Join(realHome, entry)
		if err := os.MkdirAll(target, 0o700); err != nil {
			remove()
			return "", noop, fmt.Errorf("prepare %s in real home: %w", entry, err)
		}
		if err := os.Symlink(target, filepath.Join(dir, entry)); err != nil {
			remove()
			return "", noop, fmt.Errorf("link %s into isolated home: %w", entry, err)
		}
	}
	for _, entry := range isolatedHomeFiles {
		if err := os.Symlink(filepath.Join(realHome, entry), filepath.Join(dir, entry)); err != nil {
			remove()
			return "", noop, fmt.Errorf("link %s into isolated home: %w", entry, err)
		}
	}
	cleanup = func() {
		restoreIsolatedState(dir, realHome)
		remove()
	}
	return dir, cleanup, nil
}

func restoreIsolatedState(dir, realHome string) {
	for _, entry := range isolatedHomeFiles {
		path := filepath.Join(dir, entry)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !info.Mode().IsRegular() {
			fmt.Fprintf(os.Stderr, "warning: %s written during the run is not a regular file; state not persisted\n", entry)
			continue
		}
		if err := restoreFileAtomically(path, filepath.Join(realHome, entry)); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not persist %s written during the run: %v\n", entry, err)
		}
	}
	for _, entry := range isolatedHomeDirs {
		info, err := os.Lstat(filepath.Join(dir, entry))
		if err == nil && info.Mode()&os.ModeSymlink == 0 {
			fmt.Fprintf(os.Stderr, "warning: %s was replaced during the run; its contents were not persisted to the real home\n", entry)
		}
	}
}

func restoreFileAtomically(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	tmp := target + ".ai-agent-restore"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func applyIsolatedHome(env []string, homeDir string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if key == "HOME" || isIsolatedXDGVar(key) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "HOME="+homeDir)
}

func isIsolatedXDGVar(key string) bool {
	for _, name := range isolatedXDGVars {
		if key == name {
			return true
		}
	}
	return false
}

func envValue(env []string, name string) string {
	prefix := name + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return entry[len(prefix):]
		}
	}
	return ""
}
