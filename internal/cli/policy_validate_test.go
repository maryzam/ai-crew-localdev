package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func writePolicyAndIdentities(t *testing.T, dir, policyJSON, identitiesJSON string) (policyPath, identitiesPath string) {
	t.Helper()
	policyPath = filepath.Join(dir, "policy.json")
	identitiesPath = filepath.Join(dir, "identities.json")
	if err := os.WriteFile(policyPath, []byte(policyJSON), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if err := os.WriteFile(identitiesPath, []byte(identitiesJSON), 0o600); err != nil {
		t.Fatalf("write identities: %v", err)
	}
	return policyPath, identitiesPath
}

const validIdentitiesForValidate = `{
  "schema_version": "ai-agent-identities/v2",
  "agents": {
    "claude": {
      "app_id": "111",
      "app_key": "/dev/null",
      "git_name": "claude[bot]",
      "git_email": "claude@bot",
      "github_host": "github.com",
      "tool": "claude-code",
      "model": "claude-sonnet-4-6"
    }
  }
}`

func TestPolicyValidateAcceptsValidPolicy(t *testing.T) {
	dir := t.TempDir()
	validPolicy := `{
  "schema_version": "2",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "claude": {
      "resources": ["github:repo:owner/repo"],
      "providers": {
        "github": {
          "installation_id": 42,
          "default_permissions": {"contents": "write", "metadata": "read"}
        }
      }
    }
  }
}`
	policyPath, identitiesPath := writePolicyAndIdentities(t, dir, validPolicy, validIdentitiesForValidate)
	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := runPolicyValidate(cmd, policyValidateOptions{policyPath: policyPath, identitiesPath: identitiesPath}, testPolicyValidator); err != nil {
		t.Fatalf("expected valid policy to pass, got error: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "valid") {
		t.Errorf("expected success message, got: %s", stdout.String())
	}
}

func TestPolicyValidateRejectsProviderConfigFailure(t *testing.T) {
	dir := t.TempDir()
	schemaOKProviderBad := `{
  "schema_version": "2",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "claude": {
      "resources": ["github:repo:owner/repo"],
      "providers": {
        "github": {
          "installation_id": 0,
          "default_permissions": {"contents": "write"}
        }
      }
    }
  }
}`
	policyPath, identitiesPath := writePolicyAndIdentities(t, dir, schemaOKProviderBad, validIdentitiesForValidate)
	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := runPolicyValidate(cmd, policyValidateOptions{policyPath: policyPath, identitiesPath: identitiesPath}, testPolicyValidator)
	if err == nil {
		t.Fatalf("expected policy with installation_id=0 to fail provider validation; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "installation_id") {
		t.Errorf("expected stderr to name the bad field, got: %s", stderr.String())
	}
}

func TestPolicyValidateRejectsMalformedResource(t *testing.T) {
	dir := t.TempDir()
	badResource := `{
  "schema_version": "2",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "claude": {
      "resources": ["github:org:acme"],
      "providers": {
        "github": {
          "installation_id": 42,
          "default_permissions": {"contents": "write"}
        }
      }
    }
  }
}`
	policyPath, identitiesPath := writePolicyAndIdentities(t, dir, badResource, validIdentitiesForValidate)
	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := runPolicyValidate(cmd, policyValidateOptions{policyPath: policyPath, identitiesPath: identitiesPath}, testPolicyValidator); err == nil {
		t.Fatalf("expected validation to fail for github:org:acme; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestPolicyValidateRejectsMissingIdentitiesFile(t *testing.T) {
	dir := t.TempDir()
	validPolicyWithExplicitAppID := `{
  "schema_version": "2",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "claude": {
      "resources": ["github:repo:owner/repo"],
      "providers": {
        "github": {
          "installation_id": 42,
          "app_id": "111",
          "default_permissions": {"contents": "write", "metadata": "read"}
        }
      }
    }
  }
}`
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(validPolicyWithExplicitAppID), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	identitiesPath := filepath.Join(dir, "missing-identities.json")

	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := runPolicyValidate(cmd, policyValidateOptions{policyPath: policyPath, identitiesPath: identitiesPath}, testPolicyValidator)
	if err == nil {
		t.Fatalf("expected missing identities file to fail; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Identities validation failed") {
		t.Errorf("expected stderr to name identities validation, got: %s", stderr.String())
	}
}

func TestPolicyValidateRejectsInvalidIdentitiesFile(t *testing.T) {
	dir := t.TempDir()
	validPolicyWithExplicitAppID := `{
  "schema_version": "2",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "claude": {
      "resources": ["github:repo:owner/repo"],
      "providers": {
        "github": {
          "installation_id": 42,
          "app_id": "111",
          "default_permissions": {"contents": "write", "metadata": "read"}
        }
      }
    }
  }
}`
	invalidIdentities := `{"schema_version":"wrong","agents":{}}`
	policyPath, identitiesPath := writePolicyAndIdentities(t, dir, validPolicyWithExplicitAppID, invalidIdentities)
	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := runPolicyValidate(cmd, policyValidateOptions{policyPath: policyPath, identitiesPath: identitiesPath}, testPolicyValidator)
	if err == nil {
		t.Fatalf("expected invalid identities file to fail; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Identities validation failed") {
		t.Errorf("expected stderr to name identities validation, got: %s", stderr.String())
	}
}
