package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
}

func validIdentities(keysDir string) *identity.IdentitiesFile {
	return &identity.IdentitiesFile{
		SchemaVersion: schema.IdentitiesSchemaV2,
		Agents: map[string]identity.AgentIdentity{
			"claude": {
				GitName:  "Claude",
				GitEmail: "claude@example.com",
				AppID:    "12345",
				AppKey:   filepath.Join(keysDir, "claude.pem"),
			},
			"codex": {
				GitName:  "Codex",
				GitEmail: "codex@example.com",
				AppID:    "67890",
				AppKey:   filepath.Join(keysDir, "codex.pem"),
			},
		},
	}
}

func TestCheckIdentitiesFile_Missing(t *testing.T) {
	dir := t.TempDir()
	result := CheckIdentitiesFile(dir)
	if result.Status != StatusFail {
		t.Errorf("expected StatusFail, got %d", result.Status)
	}
}

func TestCheckIdentitiesFile_Valid(t *testing.T) {
	dir := t.TempDir()
	ids := validIdentities(filepath.Join(dir, "keys"))
	writeJSON(t, filepath.Join(dir, "identities.json"), ids)

	result := CheckIdentitiesFile(dir)
	if result.Status != StatusPass {
		t.Errorf("expected StatusPass, got %d; message: %s; detail: %s", result.Status, result.Message, result.Detail)
	}
}

func TestCheckPEMFiles_Missing(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")
	ids := validIdentities(keysDir)
	writeJSON(t, filepath.Join(dir, "identities.json"), ids)

	results := CheckPEMFiles(dir)
	for _, r := range results {
		if r.Status != StatusFail {
			t.Errorf("expected StatusFail for missing PEM, got %d; message: %s", r.Status, r.Message)
		}
	}
}

func TestCheckPEMFiles_WrongPermissions(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}

	ids := validIdentities(keysDir)
	writeJSON(t, filepath.Join(dir, "identities.json"), ids)

	// Create PEM files with wrong permissions (0644).
	for _, name := range []string{"claude.pem", "codex.pem"} {
		path := filepath.Join(keysDir, name)
		if err := os.WriteFile(path, []byte("fake-pem"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	results := CheckPEMFiles(dir)
	for _, r := range results {
		if r.Status != StatusWarn {
			t.Errorf("expected StatusWarn for wrong perms, got %d; message: %s", r.Status, r.Message)
		}
	}
}

func TestCheckPEMFiles_CorrectPermissions(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}

	ids := validIdentities(keysDir)
	writeJSON(t, filepath.Join(dir, "identities.json"), ids)

	// Create PEM files with correct permissions.
	if err := os.WriteFile(filepath.Join(keysDir, "claude.pem"), []byte("fake-pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keysDir, "codex.pem"), []byte("fake-pem"), 0o400); err != nil {
		t.Fatal(err)
	}

	results := CheckPEMFiles(dir)
	for _, r := range results {
		if r.Status != StatusPass {
			t.Errorf("expected StatusPass, got %d; message: %s", r.Status, r.Message)
		}
	}
}

func TestCheckPEMFiles_Unreadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read any file; skipping unreadable-PEM test")
	}
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}

	ids := validIdentities(keysDir)
	writeJSON(t, filepath.Join(dir, "identities.json"), ids)

	// Create PEM files with no read permission (0200 = write-only).
	for _, name := range []string{"claude.pem", "codex.pem"} {
		path := filepath.Join(keysDir, name)
		if err := os.WriteFile(path, []byte("fake-pem"), 0o200); err != nil {
			t.Fatal(err)
		}
	}

	results := CheckPEMFiles(dir)
	for _, r := range results {
		if r.Status != StatusFail {
			t.Errorf("expected StatusFail for unreadable PEM, got %d; message: %s", r.Status, r.Message)
		}
	}
}

func TestCheckPEMFiles_EmptyAppKey(t *testing.T) {
	dir := t.TempDir()
	ids := &identity.IdentitiesFile{
		SchemaVersion: schema.IdentitiesSchemaV2,
		Agents: map[string]identity.AgentIdentity{
			"claude": {
				GitName:  "Claude",
				GitEmail: "claude@example.com",
				AppID:    "12345",
				AppKey:   "",
			},
		},
	}
	writeJSON(t, filepath.Join(dir, "identities.json"), ids)

	results := CheckPEMFiles(dir)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != StatusFail {
		t.Fatalf("expected StatusFail, got %d; message: %s", results[0].Status, results[0].Message)
	}
}

func TestCheckPEMFiles_DirectoryPath(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}

	ids := &identity.IdentitiesFile{
		SchemaVersion: schema.IdentitiesSchemaV2,
		Agents: map[string]identity.AgentIdentity{
			"claude": {
				GitName:  "Claude",
				GitEmail: "claude@example.com",
				AppID:    "12345",
				AppKey:   keysDir,
			},
		},
	}
	writeJSON(t, filepath.Join(dir, "identities.json"), ids)

	results := CheckPEMFiles(dir)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != StatusFail {
		t.Fatalf("expected StatusFail, got %d; message: %s", results[0].Status, results[0].Message)
	}
}

func TestCheckAppIDs_AllPresent(t *testing.T) {
	dir := t.TempDir()
	ids := validIdentities(filepath.Join(dir, "keys"))
	writeJSON(t, filepath.Join(dir, "identities.json"), ids)

	result := CheckAppIDs(dir)
	if result.Status != StatusPass {
		t.Errorf("expected StatusPass, got %d; message: %s", result.Status, result.Message)
	}
}

func TestCheckAppIDs_Missing(t *testing.T) {
	dir := t.TempDir()
	ids := &identity.IdentitiesFile{
		SchemaVersion: schema.IdentitiesSchemaV2,
		Agents: map[string]identity.AgentIdentity{
			"claude": {
				GitName:  "Claude",
				GitEmail: "claude@example.com",
				AppID:    "",
				AppKey:   "keys/claude.pem",
			},
		},
	}
	writeJSON(t, filepath.Join(dir, "identities.json"), ids)

	result := CheckAppIDs(dir)
	if result.Status != StatusFail {
		t.Errorf("expected StatusFail for missing app_id, got %d", result.Status)
	}
}

func TestCheckBrokerSocketDir_Writable(t *testing.T) {
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "ai-agent")

	result := CheckBrokerSocketDir(runtimeDir)
	if result.Status != StatusPass {
		t.Errorf("expected StatusPass, got %d; message: %s; detail: %s", result.Status, result.Message, result.Detail)
	}
}

func TestCheckBrokerSocketDir_NonWritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write to any directory; skipping non-writable test")
	}
	dir := t.TempDir()
	// Create parent with no write permission.
	parentDir := filepath.Join(dir, "nowrite")
	if err := os.MkdirAll(parentDir, 0o500); err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(parentDir, "ai-agent")

	result := CheckBrokerSocketDir(runtimeDir)
	if result.Status != StatusFail {
		t.Errorf("expected StatusFail for non-writable dir, got %d; message: %s", result.Status, result.Message)
	}
}

func TestCheckPolicyFile_Missing(t *testing.T) {
	dir := t.TempDir()
	result := CheckPolicyFile(dir)
	if result.Status != StatusWarn {
		t.Errorf("expected StatusWarn for missing policy, got %d", result.Status)
	}
}

func TestCheckPolicyFile_Valid(t *testing.T) {
	dir := t.TempDir()
	pf := &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaV1,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				AllowedRepos:       []string{"org/repo"},
				DefaultPermissions: map[string]string{"contents": "write"},
			},
		},
	}
	writeJSON(t, filepath.Join(dir, "policy.json"), pf)

	result := CheckPolicyFile(dir)
	if result.Status != StatusPass {
		t.Errorf("expected StatusPass, got %d; message: %s; detail: %s", result.Status, result.Message, result.Detail)
	}
}

func TestCheckPolicyFile_Warnings(t *testing.T) {
	dir := t.TempDir()
	pf := &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaV1,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				AllowedRepos:       []string{"org/repo"},
				DefaultPermissions: map[string]string{"made_up_permission": "write"},
			},
		},
	}
	writeJSON(t, filepath.Join(dir, "policy.json"), pf)

	result := CheckPolicyFile(dir)
	if result.Status != StatusWarn {
		t.Fatalf("expected StatusWarn, got %d; message: %s; detail: %s", result.Status, result.Message, result.Detail)
	}
}

func TestCheckAllowedRepos_EmptyRepos(t *testing.T) {
	dir := t.TempDir()
	pf := &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaV1,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				AllowedRepos:       []string{},
				DefaultPermissions: map[string]string{"contents": "write"},
			},
		},
	}
	writeJSON(t, filepath.Join(dir, "policy.json"), pf)

	result := CheckAllowedRepos(dir)
	if result.Status != StatusWarn {
		t.Errorf("expected StatusWarn for empty allowed_repos, got %d; message: %s", result.Status, result.Message)
	}
}

func TestCheckAllowedRepos_NoPolicyFile(t *testing.T) {
	dir := t.TempDir()
	result := CheckAllowedRepos(dir)
	if result.Status != StatusWarn {
		t.Errorf("expected StatusWarn when no policy file, got %d", result.Status)
	}
}
