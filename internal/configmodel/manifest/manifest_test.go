package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
)

const validManifest = `{
  "schema_version": "ai-agent-manifest/v2",
  "contracts": [
    {"name": "tests", "command": "make test", "retry": "agent"},
    {"name": "lint", "command": "make lint", "retry": "never"}
  ],
  "agents": {
    "allowed": ["claude", "codex"],
    "defaults": {"claude": {"model": "claude-sonnet-5"}}
  },
  "resources": [{"uri": "langfuse:project:project-1"}],
  "caches": [{"name": "go-build", "target": "/workspace/.cache/go-build"}],
  "services": [{"name": "db"}],
  "ports": [{"number": 8080}],
  "run_modes": ["managed_run", "project_devcontainer"],
  "resource_budgets": [{"name": "project-tokens", "metric": "tokens", "measurement_source": "native_otel", "warn_at": 1000, "stop_at": 1200, "stop_policy": "stop_run"}]
}`

func TestParseAndValidateAcceptsFullManifest(t *testing.T) {
	f, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	result := Validate(f)
	if result.Errors.HasErrors() {
		t.Fatalf("validate: %s", result.Errors.Error())
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", result.Warnings)
	}
	if len(f.Contracts) != 2 || f.Contracts[0].Name != "tests" || f.Contracts[1].Retry != RetryNever {
		t.Fatalf("contracts = %#v", f.Contracts)
	}
	if f.Agents.Defaults["claude"].Model != "claude-sonnet-5" {
		t.Fatalf("agent defaults = %#v", f.Agents.Defaults)
	}
	if len(f.ResourceBudgets) != 1 || f.ResourceBudgets[0].StopAt != 1200 {
		t.Fatalf("resource budgets = %#v", f.ResourceBudgets)
	}
}

func TestParseRejectsUndeclaredFields(t *testing.T) {
	reserved := []string{
		`{"schema_version": "ai-agent-manifest/v2", "contracts": [{"name": "t", "command": "c", "timeout": "1m"}]}`,
		`{"schema_version": "ai-agent-manifest/v2", "secrets": [{"name": "github", "resource": "github:repo:owner/repo"}]}`,
		`{"schema_version": "ai-agent-manifest/v2", "services": [{"name": "db", "required": true}]}`,
		`{"schema_version": "ai-agent-manifest/v2", "ports": [{"number": 8080, "required": true}]}`,
		`{"schema_version": "ai-agent-manifest/v2", "approvals": [{"point": "run_start", "policy": "operator_invocation"}]}`,
	}
	for _, data := range reserved {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("Parse accepted undeclared field in %s", data)
		}
	}
}

func TestValidateRejectsOldManifestSchema(t *testing.T) {
	result := Validate(&File{SchemaVersion: "ai-agent-manifest/v1", Contracts: []Contract{{Name: "tests", Command: "make test"}}})
	if !result.Errors.HasErrors() || !strings.Contains(result.Errors.Error(), schema.ManifestSchemaCurrent) {
		t.Fatalf("errors = %v, want current schema requirement", result.Errors)
	}
}

func TestParseRejectsTrailingContent(t *testing.T) {
	if _, err := Parse([]byte(`{"schema_version": "ai-agent-manifest/v2"} {"more": true}`)); err == nil {
		t.Fatal("Parse accepted trailing content")
	}
}

func TestValidateRejectsInvalidDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		file  File
		field string
	}{
		{
			"wrong schema version",
			File{SchemaVersion: "ai-agent-manifest/v3"},
			"schema_version",
		},
		{
			"empty contract name",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Contracts: []Contract{{Command: "make test"}}},
			"contracts[0].name",
		},
		{
			"whitespace contract name",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Contracts: []Contract{{Name: "  ", Command: "make test"}}},
			"contracts[0].name",
		},
		{
			"contract name over budget",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Contracts: []Contract{{Name: strings.Repeat("n", MaxContractNameLength+1), Command: "make test"}}},
			"contracts[0].name",
		},
		{
			"whitespace contract command",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Contracts: []Contract{{Name: "t", Command: " \t"}}},
			"contracts[0].command",
		},
		{
			"duplicate contract name",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Contracts: []Contract{{Name: "t", Command: "a"}, {Name: "t", Command: "b"}}},
			"contracts[1].name",
		},
		{
			"empty contract command",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Contracts: []Contract{{Name: "t"}}},
			"contracts[0].command",
		},
		{
			"invalid retry",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Contracts: []Contract{{Name: "t", Command: "c", Retry: "always"}}},
			"contracts[0].retry",
		},
		{
			"empty allowed agent",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Agents: &Agents{Allowed: []string{""}}},
			"agents.allowed[0]",
		},
		{
			"allowed agent with surrounding whitespace",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Agents: &Agents{Allowed: []string{" claude "}}},
			"agents.allowed[0]",
		},
		{
			"contract name with surrounding whitespace",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Contracts: []Contract{{Name: " tests", Command: "make test"}}},
			"contracts[0].name",
		},
		{
			"whitespace allowed agent",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Agents: &Agents{Allowed: []string{" \t"}}},
			"agents.allowed[0]",
		},
		{
			"whitespace defaults key",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Agents: &Agents{Allowed: []string{" ", "codex"}, Defaults: map[string]AgentDefaults{" ": {Model: "m"}}}},
			"agents.defaults. ",
		},
		{
			"duplicate allowed agent",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Agents: &Agents{Allowed: []string{"claude", "claude"}}},
			"agents.allowed[1]",
		},
		{
			"defaults without allowed",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Agents: &Agents{Defaults: map[string]AgentDefaults{"claude": {}}}},
			"agents.allowed",
		},
		{
			"defaults for non-allowed agent",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Agents: &Agents{Allowed: []string{"codex"}, Defaults: map[string]AgentDefaults{"claude": {}}}},
			"agents.defaults.claude",
		},
		{
			"blank model default",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Agents: &Agents{Allowed: []string{"claude"}, Defaults: map[string]AgentDefaults{"claude": {Model: " "}}}},
			"agents.defaults.claude.model",
		},
		{
			"invalid resource uri",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Resources: []Resource{{URI: "github repo owner/repo"}}},
			"resources[0].uri",
		},
		{
			"relative cache target",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Caches: []Cache{{Name: "go", Target: "workspace/.cache"}}},
			"caches[0].target",
		},
		{
			"duplicate service",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Services: []Service{{Name: "db"}, {Name: "db"}}},
			"services[1].name",
		},
		{
			"invalid port",
			File{SchemaVersion: schema.ManifestSchemaCurrent, Ports: []Port{{Number: 70000}}},
			"ports[0].number",
		},
		{
			"invalid run mode",
			File{SchemaVersion: schema.ManifestSchemaCurrent, RunModes: []string{"native_host"}},
			"run_modes[0]",
		},
		{
			"unsupported budget metric",
			File{SchemaVersion: schema.ManifestSchemaCurrent, ResourceBudgets: []ResourceBudget{{Name: "cost", Metric: "cost_usd_micros", StopAt: 10}}},
			"resource_budgets[0].metric",
		},
		{
			"stop budget without threshold",
			File{SchemaVersion: schema.ManifestSchemaCurrent, ResourceBudgets: []ResourceBudget{{Name: "tokens", Metric: BudgetMetricTokens, StopPolicy: BudgetStopPolicyStopRun}}},
			"resource_budgets[0].stop_at",
		},
		{
			"warn only budget with stop threshold",
			File{SchemaVersion: schema.ManifestSchemaCurrent, ResourceBudgets: []ResourceBudget{{Name: "tokens", Metric: BudgetMetricTokens, StopAt: 10, StopPolicy: BudgetStopPolicyWarnOnly}}},
			"resource_budgets[0].stop_at",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Validate(&tt.file)
			if !result.Errors.HasErrors() {
				t.Fatal("expected validation errors")
			}
			if !strings.Contains(result.Errors.Error(), tt.field) {
				t.Fatalf("errors %q do not mention field %q", result.Errors.Error(), tt.field)
			}
		})
	}
}

func TestValidateWarnsOnEmptyManifest(t *testing.T) {
	result := Validate(&File{SchemaVersion: schema.ManifestSchemaCurrent})
	if result.Errors.HasErrors() {
		t.Fatalf("empty manifest must be valid, got %s", result.Errors.Error())
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one no-effect warning", result.Warnings)
	}
}

func TestFindDiscoversManifestInRepoRoot(t *testing.T) {
	root := t.TempDir()
	if _, ok, err := Find(root); ok || err != nil {
		t.Fatalf("Find in empty repo = %v, %v; a genuinely missing manifest is not an error", ok, err)
	}

	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	path := PathIn(root)
	if err := os.WriteFile(path, []byte(validManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	found, ok, err := Find(root)
	if !ok || err != nil || found != path {
		t.Fatalf("Find = %q, %v, %v; want %q, true, nil", found, ok, err, path)
	}

	f, err := Load(found)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if result := Validate(f); result.Errors.HasErrors() {
		t.Fatalf("validate: %s", result.Errors.Error())
	}
}

func TestLoadAndFindRejectNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "elsewhere.json")
	if err := os.WriteFile(target, []byte(validManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	path := PathIn(root)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := Find(root); ok || err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("Find on symlinked manifest = %v, %v; must fail closed, not report absent", ok, err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("Load = %v, want regular-file rejection for symlink", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Find(root); ok || err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("Find on FIFO manifest = %v, %v; must fail closed, not report absent", ok, err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("Load = %v, want regular-file rejection for FIFO", err)
	}
}

func TestLoadRejectsOversizedManifest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, FileName)
	big := append([]byte(`{"schema_version": "`), make([]byte, maxManifestBytes)...)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Load = %v, want size limit error", err)
	}
}
