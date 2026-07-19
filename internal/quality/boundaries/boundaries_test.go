package boundaries

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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

func TestManagedRuntimeMarkerHasOneReader(t *testing.T) {
	root := repoRoot(t)
	owner := filepath.Join(root, "internal", "platform", "runenv", "runenv.go")
	for _, dir := range []string{filepath.Join(root, "cmd"), filepath.Join(root, "internal")} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == owner {
				return nil
			}
			if fileReadsManagedRuntimeMarker(t, path) {
				t.Errorf("%s reads the managed-runtime marker directly; use internal/platform/runenv as the single owner", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func fileReadsManagedRuntimeMarker(t *testing.T, path string) bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isOSEnvRead(call.Fun) || len(call.Args) == 0 {
			return true
		}
		if isManagedRuntimeMarkerArg(call.Args[0]) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isOSEnvRead(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "os" && (selector.Sel.Name == "Getenv" || selector.Sel.Name == "LookupEnv")
}

func isManagedRuntimeMarkerArg(expr ast.Expr) bool {
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		ident, ok := selector.X.(*ast.Ident)
		return ok && ident.Name == "paths" && selector.Sel.Name == "EnvContainer"
	}
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	return err == nil && value == "AI_AGENT_CONTAINER"
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
