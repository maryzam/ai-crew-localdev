package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var isolatedHomeAllowlist = []string{".claude", ".claude.json", ".codex", ".agents"}

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
	cleanup = func() { _ = os.RemoveAll(dir) }

	if realHome == "" {
		return dir, cleanup, nil
	}
	for _, entry := range isolatedHomeAllowlist {
		target := filepath.Join(realHome, entry)
		if err := os.Symlink(target, filepath.Join(dir, entry)); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("link %s into isolated home: %w", entry, err)
		}
	}
	return dir, cleanup, nil
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
