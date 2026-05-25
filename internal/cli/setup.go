package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive first-time setup for agent identities and policy",
	Long: `Walks through configuration interactively:
  1. Prompts for agent name, GitHub App credentials, and git identity
  2. Queries the GitHub API to discover installation IDs automatically
  3. Lists accessible repositories and lets you select which ones to allow
  4. Generates identities.json and policy.json

Run this once after creating your GitHub App. You can run it again to add
more agents to an existing configuration.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runSetup,
}

// Test seams for the setup command.
var (
	setupStdin      io.Reader = os.Stdin
	setupGitHubClient         = func() *broker.GitHubClient { return broker.NewGitHubClient("") }
)

func init() {
	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
	w := cmd.OutOrStdout()
	scanner := bufio.NewScanner(setupStdin)

	_, _ = fmt.Fprintln(w, "ai-agent setup — interactive first-time configuration")
	_, _ = fmt.Fprintln(w, "")

	// 1. Agent name.
	agentName, err := promptRequired(w, scanner, "Agent name (e.g. claude, codex)")
	if err != nil {
		return err
	}

	// 2. GitHub App credentials.
	appID, err := promptRequired(w, scanner, "GitHub App ID")
	if err != nil {
		return err
	}

	pemPath, err := promptRequired(w, scanner, "Path to PEM private key")
	if err != nil {
		return err
	}
	pemPath = config.ExpandHome(pemPath)
	if _, err := os.Stat(pemPath); err != nil {
		return fmt.Errorf("PEM file not found: %s", pemPath)
	}

	// 3. Git identity.
	gitName, err := promptDefault(w, scanner, "Git author name", agentName+"[bot]")
	if err != nil {
		return err
	}
	gitEmail, err := promptDefault(w, scanner, "Git author email", agentName+"@users.noreply.github.com")
	if err != nil {
		return err
	}

	// 4. Build a temporary signer to query GitHub API.
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "querying GitHub API to discover installations...")

	tmpIdent := &identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents: map[string]identity.AgentIdentity{
			agentName: {
				GitName:  gitName,
				GitEmail: gitEmail,
				AppID:    appID,
				AppKey:   pemPath,
			},
		},
	}

	signer, err := broker.NewSigner(tmpIdent)
	if err != nil {
		return fmt.Errorf("failed to load PEM key: %w", err)
	}

	jwt, err := signer.SignJWT(appID)
	if err != nil {
		return fmt.Errorf("failed to sign JWT: %w", err)
	}

	gh := setupGitHubClient()
	ctx := context.Background()

	// 5. List installations — auto-discover installation_id.
	installations, err := gh.ListInstallations(ctx, jwt)
	if err != nil {
		return fmt.Errorf("failed to list installations: %w — verify your App ID and PEM key are correct", err)
	}

	if len(installations) == 0 {
		return fmt.Errorf("no installations found for this GitHub App — install it on at least one account first")
	}

	var installation broker.Installation
	if len(installations) == 1 {
		installation = installations[0]
		_, _ = fmt.Fprintf(w, "found installation: %s (ID %d)\n", installation.Account.Login, installation.ID)
	} else {
		_, _ = fmt.Fprintln(w, "")
		_, _ = fmt.Fprintln(w, "multiple installations found:")
		for i, inst := range installations {
			_, _ = fmt.Fprintf(w, "  %d. %s (ID %d)\n", i+1, inst.Account.Login, inst.ID)
		}
		choice, err := promptRequired(w, scanner, "select installation number")
		if err != nil {
			return err
		}
		idx, err := strconv.Atoi(strings.TrimSpace(choice))
		if err != nil || idx < 1 || idx > len(installations) {
			return fmt.Errorf("invalid selection: %s", choice)
		}
		installation = installations[idx-1]
	}

	installID := installation.ID

	// 6. Mint a minimal token to list repos.
	_, _ = fmt.Fprintln(w, "listing accessible repositories...")
	tokenResp, err := gh.MintInstallationToken(ctx, jwt, installID, "", map[string]string{"metadata": "read"})
	if err != nil {
		return fmt.Errorf("failed to mint token for repo listing: %w", err)
	}

	repos, err := gh.ListInstallationRepos(ctx, tokenResp.Token)
	if err != nil {
		return fmt.Errorf("failed to list repositories: %w", err)
	}

	if len(repos) == 0 {
		return fmt.Errorf("no repositories accessible to this installation — grant repository access in GitHub App settings")
	}

	// 7. Repo selection.
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "accessible repositories:")
	for i, repo := range repos {
		vis := "public"
		if repo.Private {
			vis = "private"
		}
		_, _ = fmt.Fprintf(w, "  %d. %s (%s)\n", i+1, repo.FullName, vis)
	}
	_, _ = fmt.Fprintln(w, "")

	selInput, err := promptDefault(w, scanner, "select repos (comma-separated numbers, or 'all')", "all")
	if err != nil {
		return err
	}

	var selectedRepos []string
	if strings.EqualFold(strings.TrimSpace(selInput), "all") {
		for _, r := range repos {
			selectedRepos = append(selectedRepos, r.FullName)
		}
	} else {
		parts := strings.Split(selInput, ",")
		for _, p := range parts {
			idx, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || idx < 1 || idx > len(repos) {
				return fmt.Errorf("invalid repo selection: %s", strings.TrimSpace(p))
			}
			selectedRepos = append(selectedRepos, repos[idx-1].FullName)
		}
	}

	// 8. Generate config files.
	_, _ = fmt.Fprintln(w, "")

	identitiesPath := config.ExpandHome(config.DefaultIdentitiesPath())
	policyPath := config.ExpandHome(config.DefaultPolicyPath())

	// Load existing identities if present, or create new.
	idents := &identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents:        make(map[string]identity.AgentIdentity),
	}
	if _, err := os.Stat(identitiesPath); err == nil {
		existing, err := identity.Load(identitiesPath)
		if err != nil {
			return fmt.Errorf("existing identities file is invalid: %w — fix or remove %s before running setup", err, identitiesPath)
		}
		idents = existing
	}

	idents.Agents[agentName] = identity.AgentIdentity{
		GitName:        gitName,
		GitEmail:       gitEmail,
		GithubHost:     "github.com",
		AppID:          appID,
		AppKey:         pemPath,
		InstallationID: &installID,
	}

	pol, err := loadOrGeneratePolicy(policyPath, idents)
	if err != nil {
		return err
	}

	resources := make([]string, 0, len(selectedRepos))
	for _, r := range selectedRepos {
		resources = append(resources, "github:repo:"+r)
	}
	githubSection, err := json.Marshal(map[string]any{
		"installation_id":     installID,
		"default_permissions": policy.DefaultPermissions(),
	})
	if err != nil {
		return fmt.Errorf("encode github section: %w", err)
	}
	pol.Agents[agentName] = policy.AgentPolicy{
		Resources: resources,
		Providers: map[string]json.RawMessage{"github": githubSection},
	}

	if err := broker.ValidatePolicy(pol, validatorProviders(idents)); err != nil {
		return fmt.Errorf("refusing to write invalid policy to %s: %w", policyPath, err)
	}

	configDir := filepath.Dir(identitiesPath)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	if err := writeJSON(identitiesPath, idents); err != nil {
		return fmt.Errorf("write identities: %w", err)
	}
	_, _ = fmt.Fprintf(w, "wrote %s\n", identitiesPath)

	if err := writeJSON(policyPath, pol); err != nil {
		return fmt.Errorf("write policy: %w", err)
	}
	_, _ = fmt.Fprintf(w, "wrote %s\n", policyPath)

	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintf(w, "setup complete for agent %q (%d repos)\n", agentName, len(selectedRepos))
	_, _ = fmt.Fprintln(w, "next: run 'ai-agent up --workspace ~/github' to start the dev environment")
	return nil
}

func promptRequired(w io.Writer, scanner *bufio.Scanner, label string) (string, error) {
	_, _ = fmt.Fprintf(w, "%s: ", label)
	if !scanner.Scan() {
		return "", fmt.Errorf("unexpected end of input")
	}
	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	return val, nil
}

func promptDefault(w io.Writer, scanner *bufio.Scanner, label, def string) (string, error) {
	_, _ = fmt.Fprintf(w, "%s [%s]: ", label, def)
	if !scanner.Scan() {
		return "", fmt.Errorf("unexpected end of input")
	}
	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		return def, nil
	}
	return val, nil
}

func loadOrGeneratePolicy(path string, idents *identity.IdentitiesFile) (*policy.PolicyFile, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return policy.GenerateDefault(idents), nil
		}
		return nil, fmt.Errorf("stat policy %s: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy %s: %w", path, err)
	}
	pol, err := policy.ParsePolicy(data)
	if err != nil {
		return nil, fmt.Errorf("existing policy file %s is invalid: %w; fix or remove it before running setup", path, err)
	}
	if err := broker.ValidatePolicy(pol, validatorProviders(idents)); err != nil {
		return nil, fmt.Errorf("existing policy file %s failed validation: %w; fix or remove it before running setup", path, err)
	}
	return pol, nil
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
