package uphost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/governance"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
)

func TestLoadLangfuseEnvironmentReturnsOnlyBrokerMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	data := []byte("LANGFUSE_INIT_PROJECT_ID=managed-runs\nLANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk-test\nLANGFUSE_INIT_PROJECT_SECRET_KEY='sk-test'\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := loadLangfuseClientEnvironment(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Project != "managed-runs" || config.Endpoint != "http://host.containers.internal:3000/api/public/otel" || config.Resource != "langfuse:project:managed-runs" {
		t.Fatalf("config = %#v", config)
	}
	if strings.Contains(config.Project+config.Endpoint+config.Resource, "pk-test") || strings.Contains(config.Project+config.Endpoint+config.Resource, "sk-test") {
		t.Fatal("credential leaked into client metadata")
	}
}

func TestConfigureLangfusePolicyStoresReferencesWithoutKeys(t *testing.T) {
	dir := t.TempDir()
	identitiesPath := filepath.Join(dir, "identities.json")
	policyPath := filepath.Join(dir, "policy.json")
	identitiesJSON := `{"schema_version":"ai-agent-identities/v2","agents":{"claude":{"app_id":"111","app_key":"/dev/null","git_name":"claude[bot]","git_email":"claude@bot","github_host":"github.com","tool":"claude-code","model":"claude-sonnet-4-6"}}}`
	policyJSON := `{"schema_version":"2","default_session_ttl":"8h","default_idle_timeout":"1h","agents":{"claude":{"resources":["github:repo:owner/repo"],"providers":{"github":{"installation_id":42,"default_permissions":{"contents":"write","metadata":"read"}}}}}}`
	if err := os.WriteFile(identitiesPath, []byte(identitiesJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte(policyJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(dir, "langfuse.env")
	if err := os.WriteFile(credentials, []byte("LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk-test\nLANGFUSE_INIT_PROJECT_SECRET_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	validator := func(*policy.PolicyFile, *identity.IdentitiesFile) error { return nil }
	config := langfuseClientConfig{Project: "managed-runs", Endpoint: "http://localhost:3000/api/public/otel", Resource: "langfuse:project:managed-runs"}
	if err := configureLangfusePolicy(credentials, config, governance.Paths{Identities: identitiesPath, Policy: policyPath}, validator); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"langfuse:project:managed-runs", `"credentials_file"`, `"endpoint"`} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("policy missing %q: %s", expected, data)
		}
	}
	for _, secret := range []string{"pk-test", "sk-test"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("policy leaked %q", secret)
		}
	}
}
