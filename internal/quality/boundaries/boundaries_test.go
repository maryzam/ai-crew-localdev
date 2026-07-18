package boundaries

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadinessDefersSecureFileValidation(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "internal", "app", "readiness")
	banned := []string{"O_NOFOLLOW", "0o077"}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, token := range banned {
			if strings.Contains(string(data), token) {
				t.Errorf("%s contains %q; readiness must call internal/platform/securefile for owner-only validation instead of re-implementing broker file rules", path, token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAssetResolutionNeverTrustsWorkingDirectory(t *testing.T) {
	guarded := []string{
		filepath.Join(repoRoot(t), "internal", "runtime", "uphost", "observability.go"),
		filepath.Join(repoRoot(t), "internal", "runtime", "devcontainer", "root.go"),
	}
	for _, file := range guarded {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "os.Getwd") {
			t.Errorf("%s reads the ambient working directory for asset resolution; trusted sources must go through assetsource, embedded by default", file)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root containing go.mod")
		}
		dir = parent
	}
}
