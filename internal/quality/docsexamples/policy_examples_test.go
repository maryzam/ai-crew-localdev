package docsexamples

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/broker/core"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/manifest"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	githubprovider "github.com/maryzam/ai-crew-localdev/internal/providers/github"
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

	providers := []port.Provider{
		githubprovider.NewValidator(func(string) string { return "123456" }),
	}
	for _, example := range examples {
		name := fmt.Sprintf("%s:%d", example.File, example.StartLine)
		t.Run(name, func(t *testing.T) {
			pf, err := parsePolicyExample([]byte(example.Body))
			if err != nil {
				t.Fatalf("parse policy example: %v", err)
			}
			if err := core.ValidatePolicy(pf, providers); err != nil {
				t.Fatalf("ValidatePolicy: %v", err)
			}
		})
	}
}

func TestManifestJSONExamplesValidate(t *testing.T) {
	root := repoRoot(t)
	examples := manifestJSONExamples(t, root)
	if len(examples) == 0 {
		t.Fatal("found no fenced JSON manifest examples in docs/**/*.md or README.md")
	}

	for _, example := range examples {
		name := fmt.Sprintf("%s:%d", example.File, example.StartLine)
		t.Run(name, func(t *testing.T) {
			file, err := manifest.Parse([]byte(example.Body))
			if err != nil {
				t.Fatalf("parse manifest example: %v", err)
			}
			if result := manifest.Validate(file); result.Errors.HasErrors() {
				t.Fatalf("validate manifest example: %v", result.Errors)
			}
		})
	}
}

func parsePolicyExample(data []byte) (*policy.PolicyFile, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var pf policy.PolicyFile
	if err := decoder.Decode(&pf); err != nil {
		return nil, fmt.Errorf("strict JSON decode: %w", err)
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("strict JSON decode: multiple JSON values")
		}
		return nil, fmt.Errorf("strict JSON decode: %w", err)
	}

	return &pf, nil
}

func TestParsePolicyExampleRejectsUnknownAgentFields(t *testing.T) {
	body := []byte(`{
  "schema_version": "2",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "claude": {
      "allowed_repos": ["owner/repo"],
      "resources": ["github:repo:owner/repo"],
      "providers": {
        "github": {
          "installation_id": 123456,
          "default_permissions": {"contents": "write"}
        }
      }
    }
  }
}`)
	if _, err := parsePolicyExample(body); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("parsePolicyExample error = %v, want unknown field rejection", err)
	}
}

func TestLooksLikePolicyDocumentKeepsMalformedPolicyCandidates(t *testing.T) {
	body := `{
  "agents": {
    "claude": {
      "resources": ["github:repo:owner/repo"],
    }
  }
}`
	if !looksLikePolicyDocument(body) {
		t.Fatal("malformed policy-like JSON block should be validated, not skipped")
	}
}

func policyJSONExamples(t *testing.T, root string) []fencedBlock {
	t.Helper()

	var examples []fencedBlock
	for _, block := range docsJSONBlocks(t, root) {
		if looksLikePolicyDocument(block.Body) {
			examples = append(examples, block)
		}
	}
	return examples
}

func manifestJSONExamples(t *testing.T, root string) []fencedBlock {
	t.Helper()

	var examples []fencedBlock
	for _, block := range docsJSONBlocks(t, root) {
		if strings.Contains(block.Body, `"schema_version"`) && strings.Contains(block.Body, `ai-agent-manifest/`) {
			examples = append(examples, block)
		}
	}
	return examples
}

func docsJSONBlocks(t *testing.T, root string) []fencedBlock {
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

	var blocks []fencedBlock
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatalf("relative path for %s: %v", file, err)
		}
		blocks = append(blocks, fencedJSONBlocks(rel, string(data))...)
	}
	return blocks
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
	if strings.Contains(body, "ai-agent-identities/") {
		return false
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &top); err != nil {
		return looksLikeMalformedPolicyDocument(body)
	}

	if !strings.Contains(body, `"agents"`) {
		return false
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

func looksLikeMalformedPolicyDocument(body string) bool {
	if strings.Contains(body, `"agents"`) {
		return true
	}
	if strings.Contains(body, `"default_session_ttl"`) || strings.Contains(body, `"default_idle_timeout"`) {
		return true
	}
	return strings.Contains(body, `"schema_version"`) &&
		(strings.Contains(body, `"resources"`) || strings.Contains(body, `"providers"`))
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
