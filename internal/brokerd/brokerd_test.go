package brokerd

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

func TestValidateActivatedSocketRejectsMismatchedPaths(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "broker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	if err := validateActivatedSocket(listener.Addr(), socketPath); err != nil {
		t.Fatalf("matching activated socket rejected: %v", err)
	}

	err = validateActivatedSocket(listener.Addr(), filepath.Join(t.TempDir(), "custom.sock"))
	if err == nil || !strings.Contains(err.Error(), "does not match the configured broker socket") {
		t.Fatalf("mismatched activated socket accepted: %v", err)
	}
}

func TestRunRejectsInvalidProviderPolicyBeforeListening(t *testing.T) {
	configDir := t.TempDir()
	runtimeDir := t.TempDir()
	socketPath := filepath.Join(runtimeDir, "broker.sock")

	t.Setenv(paths.EnvConfigDir, configDir)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv(paths.EnvBrokerSocket, socketPath)
	t.Setenv(paths.EnvAuditLog, filepath.Join(configDir, "audit.log"))

	writeJSON(t, filepath.Join(configDir, "identities.json"), identity.IdentitiesFile{
		SchemaVersion: schema.IdentitiesSchemaV2,
		Agents: map[string]identity.AgentIdentity{
			"codex": {
				GitName:    "Codex Bot",
				GitEmail:   "codex@example.invalid",
				GithubHost: "github.com",
				AppID:      "fake-app-id",
				AppKey:     "fake-key",
				Tool:       "codex",
				Model:      "gpt-5-codex",
			},
		},
	})
	writeJSON(t, filepath.Join(configDir, "policy.json"), policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"codex": {
				Resources: []string{"github:repo:example-org/example-repo"},
				Providers: map[string]json.RawMessage{
					"github": mustRawSection(t, map[string]any{
						"installation_id":     0,
						"default_permissions": map[string]string{"contents": "read"},
					}),
				},
			},
		},
	})

	err := Run()
	if err == nil {
		t.Fatal("expected startup validation error")
	}
	if !strings.Contains(err.Error(), "validate policy") || !strings.Contains(err.Error(), "installation_id must be > 0") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("broker listened before rejecting policy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "audit.log")); !os.IsNotExist(err) {
		t.Fatalf("audit log created before policy rejection: %v", err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", filepath.Base(path), err)
	}
	if err := securefile.WriteOwnerOnly(path, data); err != nil {
		t.Fatalf("write %s: %v", filepath.Base(path), err)
	}
}

func mustRawSection(t *testing.T, body map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal provider section: %v", err)
	}
	return data
}
