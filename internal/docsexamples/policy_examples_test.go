package docsexamples

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	githubprovider "github.com/maryzam/ai-crew-localdev/internal/broker/providers/github"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

type fencedBlock struct {
	File      string
	StartLine int
	Body      string
}

func TestPolicyJSONExamplesValidate(t *testing.T) {
	root := repoRoot(t)
	examples := policyJSONExamples(t, root)
	if len(examples) == 0 {
		t.Fatal("found no fenced JSON policy examples in docs/**/*.md or README.md")
	}

	providers := []broker.CredentialProvider{
		githubprovider.NewValidator(func(string) string { return "123456" }),
	}
	for _, example := range examples {
		name := fmt.Sprintf("%s:%d", example.File, example.StartLine)
		t.Run(name, func(t *testing.T) {
			pf, err := policy.ParsePolicy([]byte(example.Body))
			if err != nil {
				t.Fatalf("ParsePolicy: %v", err)
			}
			if err := broker.ValidatePolicy(pf, providers); err != nil {
				t.Fatalf("ValidatePolicy: %v", err)
			}
		})
	}
}

func policyJSONExamples(t *testing.T, root string) []fencedBlock {
	t.Helper()

	var files []string
	readme := filepath.Join(root, "README.md")
	if _, err := os.Stat(readme); err == nil {
		files = append(files, readme)
	}

	docsDir := filepath.Join(root, "docs")
	err := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}

	var examples []fencedBlock
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatalf("relative path for %s: %v", file, err)
		}
		for _, block := range fencedJSONBlocks(rel, string(data)) {
			if looksLikePolicyDocument(block.Body) {
				examples = append(examples, block)
			}
		}
	}
	return examples
}

func fencedJSONBlocks(file, contents string) []fencedBlock {
	var blocks []fencedBlock
	var body []string
	inJSON := false
	startLine := 0

	for i, line := range strings.Split(contents, "\n") {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)
		if !inJSON {
			if strings.HasPrefix(trimmed, "```json") {
				inJSON = true
				startLine = lineNo + 1
				body = nil
			}
			continue
		}

		if strings.HasPrefix(trimmed, "```") {
			blocks = append(blocks, fencedBlock{
				File:      file,
				StartLine: startLine,
				Body:      strings.Join(body, "\n"),
			})
			inJSON = false
			continue
		}
		body = append(body, line)
	}
	return blocks
}

func looksLikePolicyDocument(body string) bool {
	if !strings.Contains(body, `"agents"`) {
		return false
	}
	if strings.Contains(body, "ai-agent-identities/") {
		return false
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &top); err != nil {
		return strings.Contains(body, `"schema_version"`) &&
			(strings.Contains(body, `"resources"`) || strings.Contains(body, `"providers"`))
	}

	if hasPolicyShape(top) {
		return true
	}
	var schemaVersion string
	if raw, ok := top["schema_version"]; ok && json.Unmarshal(raw, &schemaVersion) == nil {
		return schemaVersion == "2"
	}
	return false
}

func hasPolicyShape(top map[string]json.RawMessage) bool {
	rawAgents, ok := top["agents"]
	if !ok {
		return false
	}
	var agents map[string]map[string]json.RawMessage
	if err := json.Unmarshal(rawAgents, &agents); err != nil {
		return false
	}
	for _, agent := range agents {
		if _, ok := agent["resources"]; ok {
			return true
		}
		if _, ok := agent["providers"]; ok {
			return true
		}
	}
	return false
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
