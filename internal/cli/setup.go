package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/configstore"
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
	setupStdin        io.Reader = os.Stdin
	setupGitHubClient           = func() *broker.GitHubClient { return broker.NewGitHubClient("") }
)

var setupFlags struct {
	agent          string
	appID          string
	pem            string
	gitName        string
	gitEmail       string
	installationID int64
	repos          string
	nonInteractive bool
}

func init() {
	f := setupCmd.Flags()
	f.StringVar(&setupFlags.agent, "agent", "", "agent name (e.g. claude, codex)")
	f.StringVar(&setupFlags.appID, "app-id", "", "GitHub App ID")
	f.StringVar(&setupFlags.pem, "pem", "", "path to the GitHub App PEM private key")
	f.StringVar(&setupFlags.gitName, "git-name", "", "git author name (default <agent>[bot])")
	f.StringVar(&setupFlags.gitEmail, "git-email", "", "git author email (default <agent>@users.noreply.github.com)")
	f.Int64Var(&setupFlags.installationID, "installation-id", 0, "GitHub App installation ID; skips the installations API lookup")
	f.StringVar(&setupFlags.repos, "repos", "", "repos to allow: comma-separated full names or 'all'")
	f.BoolVar(&setupFlags.nonInteractive, "non-interactive", false, "fail instead of prompting; every required value must come from a flag")

	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
	return runSetupWithNext(cmd, args, nil, "next: run 'ai-agent up --workspace ~/github' to start the dev environment")
}

func runSetupWithNext(cmd *cobra.Command, args []string, scanner *bufio.Scanner, nextStep string) error {
	w := cmd.OutOrStdout()
	if scanner == nil {
		scanner = bufio.NewScanner(setupStdin)
	}
	in := newSetupInput(scanner)

	if !in.nonInteractive {
		_, _ = fmt.Fprintln(w, "ai-agent setup — interactive first-time configuration")
		_, _ = fmt.Fprintln(w, "")
	}

	// 1. Agent name.
	agentName, err := in.required(w, "agent", "Agent name (e.g. claude, codex)", setupFlags.agent)
	if err != nil {
		return err
	}

	// 2. GitHub App credentials.
	appID, err := in.required(w, "app-id", "GitHub App ID", setupFlags.appID)
	if err != nil {
		return err
	}

	pemPath, err := in.required(w, "pem", "Path to PEM private key", setupFlags.pem)
	if err != nil {
		return err
	}
	pemPath = config.ExpandHome(pemPath)
	if _, err := os.Stat(pemPath); err != nil {
		return fmt.Errorf("PEM file not found: %s", pemPath)
	}

	// 3. Git identity.
	gitName, err := in.withDefault(w, "Git author name", setupFlags.gitName, agentName+"[bot]")
	if err != nil {
		return err
	}
	gitEmail, err := in.withDefault(w, "Git author email", setupFlags.gitEmail, agentName+"@users.noreply.github.com")
	if err != nil {
		return err
	}

	// 4. Build a temporary signer to query GitHub API.
	if !in.nonInteractive {
		_, _ = fmt.Fprintln(w, "")
	}

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

	var installID int64
	if setupFlags.installationID != 0 {
		installID = setupFlags.installationID
		_, _ = fmt.Fprintf(w, "using installation ID %d\n", installID)
	} else {
		_, _ = fmt.Fprintln(w, "querying GitHub API to discover installations...")
		installations, err := gh.ListInstallations(ctx, jwt)
		if err != nil {
			return fmt.Errorf("failed to list installations: %w — verify your App ID and PEM key are correct", err)
		}
		if len(installations) == 0 {
			return fmt.Errorf("no installations found for this GitHub App — install it on at least one account first")
		}

		var installation broker.Installation
		switch {
		case len(installations) == 1:
			installation = installations[0]
			_, _ = fmt.Fprintf(w, "found installation: %s (ID %d)\n", installation.Account.Login, installation.ID)
		case in.nonInteractive:
			return fmt.Errorf("multiple installations found; pass --installation-id to select one in non-interactive mode")
		default:
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
		installID = installation.ID
	}

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
	selectedRepos, err := in.selectRepos(w, scanner, repos)
	if err != nil {
		return err
	}

	// 8. Generate config files.
	_, _ = fmt.Fprintln(w, "")

	identitiesPath := config.ExpandHome(config.DefaultIdentitiesPath())
	policyPath := configuredPolicyPath()

	// Load existing identities if present, or create new.
	idents := &identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents:        make(map[string]identity.AgentIdentity),
	}
	if _, err := os.Stat(identitiesPath); err == nil {
		existing, err := configstore.LoadIdentities(identitiesPath)
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

	pol, err := loadOrGeneratePolicy(identitiesPath, policyPath, idents)
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

	if err := configstore.Publish(identitiesPath, idents, policyPath, pol); err != nil {
		return fmt.Errorf("publish governance configuration: %w", err)
	}
	_, _ = fmt.Fprintf(w, "wrote %s\n", identitiesPath)
	_, _ = fmt.Fprintf(w, "wrote %s\n", policyPath)

	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintf(w, "setup complete for agent %q (%d repos)\n", agentName, len(selectedRepos))
	if nextStep != "" {
		_, _ = fmt.Fprintln(w, nextStep)
	}
	return nil
}

func configuredPolicyPath() string {
	path := os.Getenv("AI_AGENT_POLICY_PATH")
	if path == "" {
		path = config.DefaultPolicyPath()
	}
	return config.ExpandHome(path)
}

// setupInput resolves each value from a flag when provided, otherwise prompts.
// In non-interactive mode it never reads stdin: a missing required value is an
// error and defaults are applied silently.
type setupInput struct {
	scanner        *bufio.Scanner
	nonInteractive bool
}

func newSetupInput(scanner *bufio.Scanner) setupInput {
	return setupInput{scanner: scanner, nonInteractive: setupFlags.nonInteractive}
}

func (in setupInput) required(w io.Writer, flagName, label, flagVal string) (string, error) {
	if v := strings.TrimSpace(flagVal); v != "" {
		return v, nil
	}
	if in.nonInteractive {
		return "", fmt.Errorf("--%s is required in non-interactive mode", flagName)
	}
	return promptRequired(w, in.scanner, label)
}

func (in setupInput) withDefault(w io.Writer, label, flagVal, def string) (string, error) {
	if v := strings.TrimSpace(flagVal); v != "" {
		return v, nil
	}
	if in.nonInteractive {
		return def, nil
	}
	return promptDefault(w, in.scanner, label, def)
}

func (in setupInput) selectRepos(w io.Writer, scanner *bufio.Scanner, repos []broker.Repository) ([]string, error) {
	if sel := strings.TrimSpace(setupFlags.repos); sel != "" {
		return resolveRepoSelection(sel, repos, false)
	}
	if in.nonInteractive {
		return nil, fmt.Errorf("--repos is required in non-interactive mode (comma-separated full names or 'all')")
	}

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
		return nil, err
	}
	return resolveRepoSelection(selInput, repos, true)
}

// resolveRepoSelection turns a selection string into repo full names. When
// byIndex is true the parts are 1-based indices (interactive mode); otherwise
// they are repo full names (--repos flag).
func resolveRepoSelection(sel string, repos []broker.Repository, byIndex bool) ([]string, error) {
	if strings.EqualFold(strings.TrimSpace(sel), "all") {
		out := make([]string, 0, len(repos))
		for _, r := range repos {
			out = append(out, r.FullName)
		}
		return out, nil
	}

	var out []string
	for _, p := range strings.Split(sel, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if byIndex {
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 1 || idx > len(repos) {
				return nil, fmt.Errorf("invalid repo selection: %s", p)
			}
			out = append(out, repos[idx-1].FullName)
			continue
		}
		if !repoKnown(p, repos) {
			return nil, fmt.Errorf("repo %q is not accessible to this installation", p)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no repositories selected")
	}
	return out, nil
}

func repoKnown(fullName string, repos []broker.Repository) bool {
	for _, r := range repos {
		if r.FullName == fullName {
			return true
		}
	}
	return false
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

func loadOrGeneratePolicy(identitiesPath, path string, idents *identity.IdentitiesFile) (*policy.PolicyFile, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return policy.GenerateDefault(idents), nil
		}
		return nil, fmt.Errorf("stat policy %s: %w", path, err)
	}
	pol, err := configstore.LoadPolicy(identitiesPath, path)
	if err != nil {
		return nil, fmt.Errorf("existing policy file %s is invalid: %w; fix or remove it before running setup", path, err)
	}
	if err := broker.ValidatePolicy(pol, validatorProviders(idents)); err != nil {
		return nil, fmt.Errorf("existing policy file %s failed validation: %w; fix or remove it before running setup", path, err)
	}
	return pol, nil
}
