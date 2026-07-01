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
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	githubprovider "github.com/maryzam/ai-crew-localdev/internal/providers/github"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
	langfuseprovider "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse"
)

var setupTestServices = ProviderServices{
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
}

var setupTestOptions setupOptions

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

	origGHClient := setupTestServices.GitHubClient
	t.Cleanup(func() {
		setupTestServices.GitHubClient = origGHClient
	})
	setupTestServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	realPEM := generateTestRSAKey(t)
	pemPath2 := t.TempDir() + "/real.pem"
	if err := writeFile(pemPath2, realPEM); err != nil {
		t.Fatal(err)
	}

	input := strings.Join([]string{
		"myagent",
		"99999",
		pemPath2,
		"",
		"",
		"",
	}, "\n") + "\n"

	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader(input))

	err := runSetup(cmd, setupTestServices, setupTestOptions)
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

	origGHClient := setupTestServices.GitHubClient
	t.Cleanup(func() {
		setupTestServices.GitHubClient = origGHClient
	})
	setupTestServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	customPolicyPath := filepath.Join(t.TempDir(), "nested", "policy.json")
	t.Setenv("AI_AGENT_POLICY_PATH", customPolicyPath)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader(input))

	if err := runSetup(cmd, setupTestServices, setupTestOptions); err != nil {
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

	origGHClient := setupTestServices.GitHubClient
	t.Cleanup(func() {
		setupTestServices.GitHubClient = origGHClient
	})
	setupTestServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader(input))

	err := runSetup(cmd, setupTestServices, setupTestOptions)
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

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader(input))

	err := runSetup(cmd, setupTestServices, setupTestOptions)
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

	input := strings.Join([]string{
		"agent1",
		"111",
		pemPath,
		"",
		"",
		"2",
		"",
	}, "\n") + "\n"

	origGHClient := setupTestServices.GitHubClient
	t.Cleanup(func() {
		setupTestServices.GitHubClient = origGHClient
	})
	setupTestServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader(input))

	err := runSetup(cmd, setupTestServices, setupTestOptions)
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

	origGHClient := setupTestServices.GitHubClient
	t.Cleanup(func() {
		setupTestServices.GitHubClient = origGHClient
	})
	setupTestServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	if err := writeFile(configDir+"/identities.json", []byte("not json")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader(input))

	err := runSetup(cmd, setupTestServices, setupTestOptions)
	if err == nil {
		t.Fatal("expected error for invalid existing identities.json")
	}
	if !strings.Contains(err.Error(), "existing identities file is invalid") {
		t.Errorf("expected 'existing identities file is invalid' error, got: %v", err)
	}
}

func resetSetupFlags(t *testing.T) {
	t.Helper()
	previous := setupTestOptions
	t.Cleanup(func() { setupTestOptions = previous })
	setupTestOptions = setupOptions{}
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

	origGHClient := setupTestServices.GitHubClient
	t.Cleanup(func() {
		setupTestServices.GitHubClient = origGHClient
	})
	setupTestServices.GitHubClient = githubprovider.NewGitHubClient(server.URL)

	setupTestOptions.nonInteractive = true
	setupTestOptions.agent = "ci-agent"
	setupTestOptions.appID = "555"
	setupTestOptions.pem = pemPath
	setupTestOptions.repos = "all"

	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader(""))

	if err := runSetup(cmd, setupTestServices, setupTestOptions); err != nil {
		t.Fatalf("runSetup: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "setup complete") || !strings.Contains(out, "2 repos") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestSetupNonInteractiveMissingFlag(t *testing.T) {
	resetSetupFlags(t)

	setupTestOptions.nonInteractive = true
	setupTestOptions.agent = "ci-agent"

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader(""))

	err := runSetup(cmd, setupTestServices, setupTestOptions)
	if err == nil {
		t.Fatal("expected error for missing required flag")
	}
	if !strings.Contains(err.Error(), "--app-id is required") {
		t.Errorf("expected missing app-id error, got: %v", err)
	}
}

func writeFakePEM(path string) error {
	return writeFile(path, []byte("fake-pem-content"))
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

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
