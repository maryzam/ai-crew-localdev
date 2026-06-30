package cli

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/brokerport"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	githubprovider "github.com/maryzam/ai-crew-localdev/internal/providers/github"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
	langfuseprovider "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse"
)

func init() {
	ConfigureProviderServices(ProviderServices{
		GitHubClient: githubprovider.NewGitHubClient(""),
		NewSigner: func(identities *identity.IdentitiesFile) (JWTSigner, error) {
			return githubprovider.NewSigner(identities)
		},
		ValidatePolicy: func(policyFile *policy.PolicyFile, identities *identity.IdentitiesFile) error {
			resolver := func(agent string) string {
				if identities == nil {
					return ""
				}
				return identities.Agents[agent].AppID
			}
			return broker.ValidatePolicy(policyFile, []brokerport.CredentialProvider{githubprovider.NewValidator(resolver), langfuseprovider.New()})
		},
	})
}

// fakeSetupServer returns an httptest.Server that handles the three GitHub API
// endpoints used by the setup command: list installations, mint token, list repos.
func fakeSetupServer(t *testing.T, installID int64, repos []githubcontract.Repository) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/app/installations" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]githubcontract.Installation{
				{ID: installID, Account: struct {
					Login string `json:"login"`
				}{Login: "test-org"}},
			})
		case strings.HasSuffix(r.URL.Path, "/access_tokens") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      "ghs_fake",
				"expires_at": "2099-01-01T00:00:00Z",
			})
		case r.URL.Path == "/installation/repositories" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"repositories": repos,
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestSetupHappyPath(t *testing.T) {
	// Create a temp PEM file (content doesn't matter since we mock the signer path).
	pemPath := t.TempDir() + "/test.pem"
	if err := writeFakePEM(pemPath); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{
		{FullName: "test-org/repo-a", Private: false},
		{FullName: "test-org/repo-b", Private: true},
	}
	server := fakeSetupServer(t, 12345, repos)
	defer server.Close()

	// Provide interactive input: agent name, app id, pem path, git name (default), git email (default), repo selection (all).
	input := strings.Join([]string{
		"myagent",
		"99999",
		pemPath,
		"", // accept default git name
		"", // accept default git email
		"", // accept default repo selection (all)
	}, "\n") + "\n"

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader(input)
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	// We need to stub the signer. The real signer needs a valid RSA key.
	// For this test, create a real RSA PEM.
	realPEM := generateTestRSAKey(t)
	pemPath2 := t.TempDir() + "/real.pem"
	if err := writeFile(pemPath2, realPEM); err != nil {
		t.Fatal(err)
	}

	// Re-set input with the real PEM path.
	input = strings.Join([]string{
		"myagent",
		"99999",
		pemPath2,
		"", // accept default git name
		"", // accept default git email
		"", // accept default repo selection (all)
	}, "\n") + "\n"
	setupStdin = strings.NewReader(input)

	// Override config paths to write to temp dir.
	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err != nil {
		t.Fatalf("runSetup: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "setup complete") {
		t.Errorf("expected 'setup complete' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "myagent") {
		t.Errorf("expected agent name in output, got:\n%s", output)
	}
	if !strings.Contains(output, "2 repos") {
		t.Errorf("expected '2 repos' in output, got:\n%s", output)
	}
}

func TestSetupSelectSpecificRepos(t *testing.T) {
	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/test.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{
		{FullName: "org/alpha", Private: false},
		{FullName: "org/beta", Private: false},
		{FullName: "org/gamma", Private: true},
	}
	server := fakeSetupServer(t, 42, repos)
	defer server.Close()

	input := strings.Join([]string{
		"agent1",
		"111",
		pemPath,
		"",    // default git name
		"",    // default git email
		"1,3", // select repos 1 and 3
	}, "\n") + "\n"

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader(input)
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err != nil {
		t.Fatalf("runSetup: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "2 repos") {
		t.Errorf("expected '2 repos' in output, got:\n%s", output)
	}
}

func TestSetupWritesPolicyToConfiguredPolicyPath(t *testing.T) {
	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/test.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{{FullName: "org/repo", Private: false}}
	server := fakeSetupServer(t, 42, repos)
	defer server.Close()

	input := strings.Join([]string{
		"agent1",
		"111",
		pemPath,
		"",
		"",
		"",
	}, "\n") + "\n"

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader(input)
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	customPolicyPath := filepath.Join(t.TempDir(), "nested", "policy.json")
	t.Setenv("AI_AGENT_POLICY_PATH", customPolicyPath)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := runSetup(cmd, nil); err != nil {
		t.Fatalf("runSetup: %v", err)
	}
	if _, err := os.Stat(customPolicyPath); err != nil {
		t.Fatalf("expected policy at custom path %s: %v", customPolicyPath, err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "policy.json")); !os.IsNotExist(err) {
		t.Fatalf("default policy path should not be written when AI_AGENT_POLICY_PATH is set, stat err=%v", err)
	}
	if !strings.Contains(buf.String(), "wrote "+customPolicyPath) {
		t.Fatalf("output does not name custom policy path:\n%s", buf.String())
	}
}

func TestSetupNoInstallations(t *testing.T) {
	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/test.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]githubcontract.Installation{})
	}))
	defer server.Close()

	input := strings.Join([]string{
		"agent1",
		"111",
		pemPath,
		"",
		"",
	}, "\n") + "\n"

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader(input)
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err == nil {
		t.Fatal("expected error for no installations")
	}
	if !strings.Contains(err.Error(), "no installations found") {
		t.Errorf("expected 'no installations found' error, got: %v", err)
	}
}

func TestSetupPEMNotFound(t *testing.T) {
	input := strings.Join([]string{
		"agent1",
		"111",
		"/nonexistent/path/key.pem",
	}, "\n") + "\n"

	origStdin := setupStdin
	t.Cleanup(func() { setupStdin = origStdin })
	setupStdin = strings.NewReader(input)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing PEM")
	}
	if !strings.Contains(err.Error(), "PEM file not found") {
		t.Errorf("expected 'PEM file not found' error, got: %v", err)
	}
}

func TestSetupMultipleInstallationsSelection(t *testing.T) {
	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/test.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{{FullName: "org/repo1", Private: false}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/app/installations":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]githubcontract.Installation{
				{ID: 100, Account: struct {
					Login string `json:"login"`
				}{Login: "org-a"}},
				{ID: 200, Account: struct {
					Login string `json:"login"`
				}{Login: "org-b"}},
			})
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      "ghs_fake",
				"expires_at": "2099-01-01T00:00:00Z",
			})
		case r.URL.Path == "/installation/repositories":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"repositories": repos,
			})
		}
	}))
	defer server.Close()

	// Select installation 2 (org-b).
	input := strings.Join([]string{
		"agent1",
		"111",
		pemPath,
		"",  // default git name
		"",  // default git email
		"2", // select installation 2
		"",  // all repos
	}, "\n") + "\n"

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader(input)
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err != nil {
		t.Fatalf("runSetup: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "org-b") {
		t.Errorf("expected org-b in output, got:\n%s", output)
	}
}

func TestSetupRejectsInvalidExistingIdentities(t *testing.T) {
	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/test.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{{FullName: "org/repo", Private: false}}
	server := fakeSetupServer(t, 42, repos)
	defer server.Close()

	input := strings.Join([]string{
		"agent1",
		"111",
		pemPath,
		"",
		"",
		"",
	}, "\n") + "\n"

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader(input)
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	// Plant an invalid identities.json.
	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	if err := writeFile(configDir+"/identities.json", []byte("not json")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err == nil {
		t.Fatal("expected error for invalid existing identities.json")
	}
	if !strings.Contains(err.Error(), "existing identities file is invalid") {
		t.Errorf("expected 'existing identities file is invalid' error, got: %v", err)
	}
}

func TestSetupRejectsInvalidExistingPolicy(t *testing.T) {
	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/test.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{{FullName: "org/repo", Private: false}}
	server := fakeSetupServer(t, 42, repos)
	defer server.Close()

	input := strings.Join([]string{
		"agent1",
		"111",
		pemPath,
		"",
		"",
		"",
	}, "\n") + "\n"

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader(input)
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	// Plant a valid identities.json but invalid policy.json.
	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	if err := writeFile(configDir+"/policy.json", []byte("{bad json")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err == nil {
		t.Fatal("expected error for invalid existing policy.json")
	}
	if !strings.Contains(err.Error(), "is invalid") {
		t.Errorf("expected 'is invalid' error, got: %v", err)
	}
}

func TestSetupRejectsExistingPolicyThatFailsValidation(t *testing.T) {
	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/test.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{{FullName: "org/repo", Private: false}}
	server := fakeSetupServer(t, 42, repos)
	defer server.Close()

	input := strings.Join([]string{
		"agent1", "111", pemPath, "", "", "",
	}, "\n") + "\n"

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader(input)
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)

	parsable := `{
  "schema_version": "wrong/v99",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {}
}`
	if err := writeFile(configDir+"/policy.json", []byte(parsable)); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err == nil {
		t.Fatal("expected error for policy that fails validation")
	}
	if !strings.Contains(err.Error(), "failed validation") {
		t.Errorf("expected 'failed validation' in error, got: %v", err)
	}
}

func TestSetupRejectsExistingPolicyWithInvalidProviderConfig(t *testing.T) {
	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/test.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{{FullName: "org/repo", Private: false}}
	server := fakeSetupServer(t, 42, repos)
	defer server.Close()

	input := strings.Join([]string{
		"agent1", "111", pemPath, "", "", "",
	}, "\n") + "\n"

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader(input)
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)

	parsableButZeroInstall := `{
  "schema_version": "2",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "preexisting": {
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
	if err := writeFile(configDir+"/policy.json", []byte(parsableButZeroInstall)); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err == nil {
		t.Fatal("expected error for existing policy whose provider config is invalid")
	}
	if !strings.Contains(err.Error(), "installation_id") {
		t.Errorf("error should name the bad field, got: %v", err)
	}
}

func TestSetupRejectsWritingPolicyWithMalformedResource(t *testing.T) {
	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/test.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{{FullName: "org/repo", Private: false}}
	server := fakeSetupServer(t, 42, repos)
	defer server.Close()

	input := strings.Join([]string{
		"agent1", "111", pemPath, "", "", "",
	}, "\n") + "\n"

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader(input)
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)

	existing := `{
  "schema_version": "2",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "preexisting": {
      "resources": ["github:org:acme"],
      "providers": {
        "github": {
          "installation_id": 99,
          "default_permissions": {"contents": "write"}
        }
      }
    }
  }
}`
	if err := writeFile(configDir+"/policy.json", []byte(existing)); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err == nil {
		t.Fatal("expected error for existing policy with malformed resource URI")
	}
}

// resetSetupFlags restores the package-level setup flags after a test mutates
// them, so global flag state never leaks across test cases.
func resetSetupFlags(t *testing.T) {
	t.Helper()
	orig := setupFlags
	t.Cleanup(func() { setupFlags = orig })
	setupFlags = struct {
		agent          string
		appID          string
		pem            string
		gitName        string
		gitEmail       string
		installationID int64
		repos          string
		nonInteractive bool
	}{}
}

func TestSetupNonInteractiveHappyPath(t *testing.T) {
	resetSetupFlags(t)

	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/real.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{
		{FullName: "org/alpha", Private: false},
		{FullName: "org/beta", Private: true},
	}
	server := fakeSetupServer(t, 777, repos)
	defer server.Close()

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader("")
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	setupFlags.nonInteractive = true
	setupFlags.agent = "ci-agent"
	setupFlags.appID = "555"
	setupFlags.pem = pemPath
	setupFlags.repos = "all"

	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := runSetup(cmd, nil); err != nil {
		t.Fatalf("runSetup: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "setup complete") || !strings.Contains(out, "2 repos") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestSetupNonInteractiveWithInstallationID(t *testing.T) {
	resetSetupFlags(t)

	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/real.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{{FullName: "org/only", Private: false}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token": "ghs_fake", "expires_at": "2099-01-01T00:00:00Z",
			})
		case r.URL.Path == "/installation/repositories":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"repositories": repos})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader("")
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	setupFlags.nonInteractive = true
	setupFlags.agent = "ci-agent"
	setupFlags.appID = "555"
	setupFlags.pem = pemPath
	setupFlags.installationID = 4242
	setupFlags.repos = "org/only"

	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := runSetup(cmd, nil); err != nil {
		t.Fatalf("runSetup: %v", err)
	}
	if !strings.Contains(buf.String(), "using installation ID 4242") {
		t.Errorf("expected installation ID notice, got:\n%s", buf.String())
	}
}

func TestSetupNonInteractiveMissingFlag(t *testing.T) {
	resetSetupFlags(t)

	origStdin := setupStdin
	t.Cleanup(func() { setupStdin = origStdin })
	setupStdin = strings.NewReader("")

	setupFlags.nonInteractive = true
	setupFlags.agent = "ci-agent"

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing required flag")
	}
	if !strings.Contains(err.Error(), "--app-id is required") {
		t.Errorf("expected missing app-id error, got: %v", err)
	}
}

func TestSetupNonInteractiveUnknownRepo(t *testing.T) {
	resetSetupFlags(t)

	realPEM := generateTestRSAKey(t)
	pemPath := t.TempDir() + "/real.pem"
	if err := writeFile(pemPath, realPEM); err != nil {
		t.Fatal(err)
	}

	repos := []githubcontract.Repository{{FullName: "org/known", Private: false}}
	server := fakeSetupServer(t, 1, repos)
	defer server.Close()

	origStdin := setupStdin
	origGHClient := providerServices.GitHubClient
	t.Cleanup(func() {
		setupStdin = origStdin
		providerServices.GitHubClient = origGHClient
	})
	setupStdin = strings.NewReader("")
	providerServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	setupFlags.nonInteractive = true
	setupFlags.agent = "ci-agent"
	setupFlags.appID = "555"
	setupFlags.pem = pemPath
	setupFlags.repos = "org/does-not-exist"

	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runSetup(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("expected 'not accessible' error, got: %v", err)
	}
}

// --- Test helpers ---

func writeFakePEM(path string) error {
	return writeFile(path, []byte("fake-pem-content"))
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

// generateTestRSAKey generates a minimal RSA private key in PEM format for tests.
func generateTestRSAKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	buf := &bytes.Buffer{}
	err = pem.Encode(buf, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err != nil {
		t.Fatalf("encode PEM: %v", err)
	}
	return buf.Bytes()
}
