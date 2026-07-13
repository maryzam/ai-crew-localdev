package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/app/onboarding"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/governance"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
)

type setupOptions struct {
	agent          string
	appID          string
	pem            string
	gitName        string
	gitEmail       string
	installationID int64
	repos          string
	nonInteractive bool
}

func newSetupCommand(services ProviderServices) *cobra.Command {
	options := setupOptions{}
	command := &cobra.Command{
		Use:   "setup",
		Short: "Interactive first-time setup for agent identities and policy",
		Long: `Walks through configuration interactively:
  1. Prompts for agent name, GitHub App credentials, and git identity
  2. Queries the GitHub API to discover installation IDs automatically
  3. Lists accessible repositories and lets you select which ones to allow
  4. Generates identities.json and policy.json

Run this once after creating your GitHub App. You can run it again to add more agents to an existing configuration.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	flags := command.Flags()
	flags.StringVar(&options.agent, "agent", "", "agent name (e.g. claude, codex)")
	flags.StringVar(&options.appID, "app-id", "", "GitHub App ID")
	flags.StringVar(&options.pem, "pem", "", "path to the GitHub App PEM private key")
	flags.StringVar(&options.gitName, "git-name", "", "git author name (default <agent>[bot])")
	flags.StringVar(&options.gitEmail, "git-email", "", "git author email (default <agent>@users.noreply.github.com)")
	flags.Int64Var(&options.installationID, "installation-id", 0, "GitHub App installation ID; skips the installations API lookup")
	flags.StringVar(&options.repos, "repos", "", "repos to allow: comma-separated full names or 'all'")
	flags.BoolVar(&options.nonInteractive, "non-interactive", false, "fail instead of prompting; every required value must come from a flag")
	command.RunE = func(command *cobra.Command, args []string) error {
		return runSetup(command, services, options)
	}
	return command
}

func runSetup(cmd *cobra.Command, services ProviderServices, options setupOptions) error {
	return runSetupWithNext(cmd, nil, "next: run 'ai-agent up --workspace ~/github' to start the dev environment", services, options)
}

func runSetupWithNext(cmd *cobra.Command, scanner *bufio.Scanner, nextStep string, services ProviderServices, options setupOptions) error {
	w := cmd.OutOrStdout()
	if scanner == nil {
		scanner = bufio.NewScanner(cmd.InOrStdin())
	}
	in := newSetupInput(scanner, options.nonInteractive)

	if !in.nonInteractive {
		_, _ = fmt.Fprintln(w, "ai-agent setup — interactive first-time configuration")
		_, _ = fmt.Fprintln(w, "")
	}

	agentName, err := in.required(w, "agent", "Agent name (e.g. claude, codex)", options.agent)
	if err != nil {
		return err
	}

	appID, err := in.required(w, "app-id", "GitHub App ID", options.appID)
	if err != nil {
		return err
	}

	pemPath, err := in.required(w, "pem", "Path to PEM private key", options.pem)
	if err != nil {
		return err
	}
	pemPath = paths.ExpandHome(pemPath)
	if _, err := os.Stat(pemPath); err != nil {
		return fmt.Errorf("PEM file not found: %s", pemPath)
	}
	gitName, err := in.withDefault(w, "Git author name", options.gitName, agentName+"[bot]")
	if err != nil {
		return err
	}
	gitEmail, err := in.withDefault(w, "Git author email", options.gitEmail, agentName+"@users.noreply.github.com")
	if err != nil {
		return err
	}

	if !in.nonInteractive {
		_, _ = fmt.Fprintln(w, "")
	}
	governancePaths := governance.DefaultPaths()
	useCase := onboarding.New(onboarding.Dependencies{
		GitHub: services.GitHubClient,
		NewSigner: func(identities *identity.IdentitiesFile) (onboarding.Signer, error) {
			return services.NewSigner(identities)
		},
		ValidatePolicy: services.ValidatePolicy,
		Store:          governance.FileStore{},
	})
	result, err := useCase.Run(commandContext(cmd), onboarding.Input{
		AgentName:      agentName,
		AppID:          appID,
		PEMPath:        pemPath,
		GitName:        gitName,
		GitEmail:       gitEmail,
		InstallationID: options.installationID,
		RepoSelection:  options.repos,
		NonInteractive: in.nonInteractive,
		IdentitiesPath: governancePaths.Identities,
		PolicyPath:     governancePaths.Policy,
	}, setupInteraction{writer: w, scanner: scanner})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "wrote %s\n", result.IdentitiesPath)
	_, _ = fmt.Fprintf(w, "wrote %s\n", result.PolicyPath)

	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintf(w, "setup complete for agent %q (%d repos)\n", result.AgentName, result.RepositoryCount)
	if nextStep != "" {
		_, _ = fmt.Fprintln(w, nextStep)
	}
	return nil
}

func commandContext(command *cobra.Command) context.Context {
	if command.Context() != nil {
		return command.Context()
	}
	return context.Background()
}

type setupInput struct {
	scanner        *bufio.Scanner
	nonInteractive bool
}

func newSetupInput(scanner *bufio.Scanner, nonInteractive bool) setupInput {
	return setupInput{scanner: scanner, nonInteractive: nonInteractive}
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

type setupInteraction struct {
	writer  io.Writer
	scanner *bufio.Scanner
}

func (interaction setupInteraction) UsingInstallation(installationID int64) {
	_, _ = fmt.Fprintf(interaction.writer, "using installation ID %d\n", installationID)
}

func (interaction setupInteraction) QueryingInstallations() {
	_, _ = fmt.Fprintln(interaction.writer, "querying GitHub API to discover installations...")
}

func (interaction setupInteraction) FoundInstallation(installation githubcontract.Installation) {
	_, _ = fmt.Fprintf(interaction.writer, "found installation: %s (ID %d)\n", installation.Account.Login, installation.ID)
}

func (interaction setupInteraction) ChooseInstallation(installations []githubcontract.Installation) (string, error) {
	_, _ = fmt.Fprintln(interaction.writer, "")
	_, _ = fmt.Fprintln(interaction.writer, "multiple installations found:")
	for index, installation := range installations {
		_, _ = fmt.Fprintf(interaction.writer, "  %d. %s (ID %d)\n", index+1, installation.Account.Login, installation.ID)
	}
	return promptRequired(interaction.writer, interaction.scanner, "select installation number")
}

func (interaction setupInteraction) ListingRepositories() {
	_, _ = fmt.Fprintln(interaction.writer, "listing accessible repositories...")
}

func (interaction setupInteraction) ChooseRepositories(repositories []githubcontract.Repository) (string, error) {
	_, _ = fmt.Fprintln(interaction.writer, "")
	_, _ = fmt.Fprintln(interaction.writer, "accessible repositories:")
	for index, repository := range repositories {
		visibility := "public"
		if repository.Private {
			visibility = "private"
		}
		_, _ = fmt.Fprintf(interaction.writer, "  %d. %s (%s)\n", index+1, repository.FullName, visibility)
	}
	_, _ = fmt.Fprintln(interaction.writer, "")
	return promptDefault(interaction.writer, interaction.scanner, "select repos (comma-separated numbers, or 'all')", "all")
}

func (interaction setupInteraction) PreparingConfiguration() {
	_, _ = fmt.Fprintln(interaction.writer, "")
}
