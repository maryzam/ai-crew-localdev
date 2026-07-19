package securityclaims

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryProofsResolveToExistingNamedTests(t *testing.T) {
	root := repoRoot(t)
	seenIDs := map[int]struct{}{}
	for _, invariant := range Invariants() {
		if invariant.ID <= 0 {
			t.Fatalf("invalid invariant ID %d", invariant.ID)
		}
		if _, dup := seenIDs[invariant.ID]; dup {
			t.Fatalf("duplicate invariant ID %d", invariant.ID)
		}
		seenIDs[invariant.ID] = struct{}{}
		if invariant.Claim == "" || invariant.Enforcement == "" {
			t.Fatalf("invariant %d must have claim and enforcement text", invariant.ID)
		}
		if len(invariant.Proofs) == 0 {
			t.Fatalf("invariant %d has no executable proof", invariant.ID)
		}
		for _, proof := range invariant.Proofs {
			if !testFunctionExists(t, filepath.Join(root, proof.Path), proof.Test) {
				t.Fatalf("invariant %d proof %s:%s does not resolve to a test", invariant.ID, proof.Path, proof.Test)
			}
		}
	}
	for i := 1; i <= len(seenIDs); i++ {
		if _, ok := seenIDs[i]; !ok {
			t.Fatalf("missing invariant ID %d", i)
		}
	}
}

func TestSecurityDocsMatchGeneratedRegistry(t *testing.T) {
	root := repoRoot(t)
	for _, block := range generatedBlocks() {
		block.Path = filepath.Join(root, block.Path)
		if err := checkBlock(block); err != nil {
			t.Fatal(err)
		}
	}
}

func testFunctionExists(t *testing.T, path string, name string) bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return true
		}
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
