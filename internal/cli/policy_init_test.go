package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func writeIdentitiesForInit(t *testing.T, dir string) string {
	t.Helper()
	installID := int64(42)
	idents := &identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents: map[string]identity.AgentIdentity{
			"claude": {
				AppID:          "111",
				AppKey:         filepath.Join(dir, "claude.pem"),
				GitName:        "claude[bot]",
				GitEmail:       "claude@bot",
				GithubHost:     "github.com",
				Tool:           "claude-code",
				Model:          "test",
				InstallationID: &installID,
			},
		},
	}
	if err := os.WriteFile(idents.Agents["claude"].AppKey, []byte("fake-pem"), 0600); err != nil {
		t.Fatal(err)
	}

	idPath := filepath.Join(dir, "identities.json")
	data, _ := json.MarshalIndent(idents, "", "  ")
	if err := os.WriteFile(idPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	return idPath
}

func resetPolicyInitFlags() {
	initOutput = ""
	initForce = false
	initIdentities = ""
	initDraft = false
}

func TestPolicyInitRefusesToWriteIncompletePolicy(t *testing.T) {
	resetPolicyInitFlags()
	t.Cleanup(resetPolicyInitFlags)

	dir := t.TempDir()
	idPath := writeIdentitiesForInit(t, dir)
	output := filepath.Join(dir, "policy.json")

	initIdentities = idPath
	initOutput = output

	cmd := &cobra.Command{}
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	err := runPolicyInit(cmd, nil)
	if err == nil {
		t.Fatal("expected error when generated policy fails validation")
	}
	if _, statErr := os.Stat(output); statErr == nil {
		t.Errorf("policy file should not have been written, but %s exists", output)
	}
	if !strings.Contains(stderr.String(), "--draft") {
		t.Errorf("guidance should mention --draft, got: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "ai-agent setup") {
		t.Errorf("guidance should mention ai-agent setup, got: %s", stderr.String())
	}
}

func TestPolicyInitDraftWritesWithWarning(t *testing.T) {
	resetPolicyInitFlags()
	t.Cleanup(resetPolicyInitFlags)

	dir := t.TempDir()
	idPath := writeIdentitiesForInit(t, dir)
	output := filepath.Join(dir, "policy.json")

	initIdentities = idPath
	initOutput = output
	initDraft = true

	cmd := &cobra.Command{}
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	if err := runPolicyInit(cmd, nil); err != nil {
		t.Fatalf("policy init --draft should succeed: %v", err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("expected policy file at %s: %v", output, err)
	}
	if !strings.Contains(stdout.String(), "draft") {
		t.Errorf("output should warn about draft state, got: %s", stdout.String())
	}
}

func TestPolicyInitUsesGovernanceDefaultPaths(t *testing.T) {
	resetPolicyInitFlags()
	t.Cleanup(resetPolicyInitFlags)

	configDir := t.TempDir()
	customPolicyPath := filepath.Join(t.TempDir(), "custom-policy.json")
	t.Setenv(paths.EnvConfigDir, configDir)
	t.Setenv(paths.EnvPolicyPath, customPolicyPath)
	writeIdentitiesForInit(t, configDir)
	initDraft = true

	cmd := &cobra.Command{}
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	if err := runPolicyInit(cmd, nil); err != nil {
		t.Fatalf("policy init --draft should use governance defaults: %v", err)
	}
	if _, err := os.Stat(customPolicyPath); err != nil {
		t.Fatalf("expected policy file at %s: %v", customPolicyPath, err)
	}
	if _, err := os.Stat(paths.DefaultPolicyPath()); !os.IsNotExist(err) {
		t.Fatalf("default policy path should not be written when AI_AGENT_POLICY_PATH is set, stat err=%v", err)
	}
	if !strings.Contains(stdout.String(), customPolicyPath) {
		t.Fatalf("stdout %q does not mention custom policy path %s", stdout.String(), customPolicyPath)
	}
}

func TestPolicyInitDoesNotRewriteIdentities(t *testing.T) {
	resetPolicyInitFlags()
	t.Cleanup(resetPolicyInitFlags)

	dir := t.TempDir()
	identitiesPath := filepath.Join(dir, "identities.json")
	policyPath := filepath.Join(dir, "policy.json")
	identitiesData := []byte(`{"schema_version":"ai-agent-identities/v2","future_field":"preserve","agents":{"claude":{"app_id":"111","app_key":"/dev/null","git_name":"claude[bot]","git_email":"claude@example.test","github_host":"github.com","tool":"claude-code","model":"test","future_agent_field":"preserve"}}}`)
	if err := os.WriteFile(identitiesPath, identitiesData, 0o600); err != nil {
		t.Fatal(err)
	}
	initIdentities = identitiesPath
	initOutput = policyPath
	initDraft = true

	cmd := &cobra.Command{}
	if err := runPolicyInit(cmd, nil); err != nil {
		t.Fatalf("policy init --draft should succeed: %v", err)
	}
	got, err := os.ReadFile(identitiesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(identitiesData) {
		t.Fatalf("identities changed:\n got %s\nwant %s", got, identitiesData)
	}
}
