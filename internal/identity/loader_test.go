package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func testdataPath(name string) string {

	return filepath.Join("..", "..", "testdata", "identities", name)
}

func secureTestdataPath(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(testdataPath(name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_ValidV2(t *testing.T) {
	f, err := Load(secureTestdataPath(t, "valid_v2.json"))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if len(f.Agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(f.Agents))
	}
	if f.Agents["codex"].AppID != "12345" {
		t.Errorf("expected codex app_id=12345, got %q", f.Agents["codex"].AppID)
	}
	if errs := Validate(f); errs.HasErrors() {
		t.Errorf("valid file errors: %v", errs)
	}
}

func TestLoad_MissingAgents(t *testing.T) {
	f, err := Load(secureTestdataPath(t, "invalid_missing_agents.json"))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	errs := Validate(f)
	if !errs.HasErrors() {
		t.Error("expected validation errors for missing agents")
	}
	found := false
	for _, e := range errs {
		if e.Field == "agents" {
			found = true
		}
	}
	if !found {
		t.Error("expected error on 'agents' field")
	}
}

func TestLoad_MissingAppID(t *testing.T) {

	data := []byte(`{
		"schema_version": "ai-agent-identities/v2",
		"agents": {
			"codex": {
				"git_name": "codex[bot]",
				"git_email": "codex@users.noreply.github.com",
				"github_host": "github.com",
				"app_id": "",
				"app_key": "key.pem",
				"tool": "codex",
				"model": "o3"
			}
		}
	}`)
	tmp := filepath.Join(t.TempDir(), "missing_app_id.json")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		t.Fatal(err)
	}

	f, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	errs := Validate(f)
	if !errs.HasErrors() {
		t.Error("expected validation errors for missing app_id")
	}
	found := false
	for _, e := range errs {
		if e.Field == "agents.codex.app_id" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on app_id field, got: %v", errs)
	}
}

func TestLoad_WrongSchemaVersion(t *testing.T) {
	data := []byte(`{
		"schema_version": "ai-agent-identities/v1",
		"agents": {
			"codex": {
				"git_name": "codex[bot]",
				"git_email": "codex@test.com",
				"app_id": "123"
			}
		}
	}`)
	tmp := filepath.Join(t.TempDir(), "wrong_version.json")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(tmp)
	if err == nil {
		t.Error("expected error for wrong schema version")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/identities.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
