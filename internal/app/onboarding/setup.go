package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/store"
	"github.com/maryzam/ai-crew-localdev/internal/providers/capabilities"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
)

type GitHub interface {
	ListInstallations(context.Context, string) ([]githubcontract.Installation, error)
	MintInstallationToken(context.Context, string, int64, string, map[string]string) (*githubcontract.InstallationToken, error)
	ListInstallationRepos(context.Context, string) ([]githubcontract.Repository, error)
}

type Signer interface {
	SignJWT(string) (string, error)
}

type GovernanceStore interface {
	Load(string, string) (StoredGovernance, error)
	Publish(string, *identity.IdentitiesFile, string, *policy.PolicyFile) error
}

type StoredGovernance struct {
	Identities      *identity.IdentitiesFile
	Policy          *policy.PolicyFile
	IdentitiesError error
	PolicyError     error
}

type Interaction interface {
	UsingInstallation(int64)
	QueryingInstallations()
	FoundInstallation(githubcontract.Installation)
	ChooseInstallation([]githubcontract.Installation) (string, error)
	ListingRepositories()
	ChooseRepositories([]githubcontract.Repository) (string, error)
	PreparingConfiguration()
}

type Dependencies struct {
	GitHub         GitHub
	NewSigner      func(*identity.IdentitiesFile) (Signer, error)
	ValidatePolicy func(*policy.PolicyFile, *identity.IdentitiesFile) error
	Store          GovernanceStore
}

type Input struct {
	AgentName      string
	AppID          string
	PEMPath        string
	GitName        string
	GitEmail       string
	InstallationID int64
	RepoSelection  string
	NonInteractive bool
	IdentitiesPath string
	PolicyPath     string
}

type Result struct {
	AgentName       string
	RepositoryCount int
	IdentitiesPath  string
	PolicyPath      string
}

type UseCase struct {
	dependencies Dependencies
}

type FileStore struct{}

func New(dependencies Dependencies) *UseCase {
	return &UseCase{dependencies: dependencies}
}

func (FileStore) Load(identitiesPath, policyPath string) (StoredGovernance, error) {
	snapshot, err := store.Load(identitiesPath, policyPath)
	if err != nil {
		return StoredGovernance{}, err
	}
	return StoredGovernance{Identities: snapshot.Identities, Policy: snapshot.Policy, IdentitiesError: snapshot.IdentitiesError, PolicyError: snapshot.PolicyError}, nil
}

func (FileStore) Publish(identitiesPath string, identities *identity.IdentitiesFile, policyPath string, policyFile *policy.PolicyFile) error {
	return store.Publish(identitiesPath, identities, policyPath, policyFile)
}

func (useCase *UseCase) Run(ctx context.Context, input Input, interaction Interaction) (Result, error) {
	if err := useCase.validateDependencies(interaction); err != nil {
		return Result{}, err
	}
	temporaryIdentities := identitiesFor(input, 0)
	signer, err := useCase.dependencies.NewSigner(temporaryIdentities)
	if err != nil {
		return Result{}, fmt.Errorf("failed to load PEM key: %w", err)
	}
	jwt, err := signer.SignJWT(input.AppID)
	if err != nil {
		return Result{}, fmt.Errorf("failed to sign JWT: %w", err)
	}

	installationID, err := useCase.resolveInstallation(ctx, jwt, input, interaction)
	if err != nil {
		return Result{}, err
	}
	interaction.ListingRepositories()
	token, err := useCase.dependencies.GitHub.MintInstallationToken(ctx, jwt, installationID, "", map[string]string{"metadata": "read"})
	if err != nil {
		return Result{}, fmt.Errorf("failed to mint token for repo listing: %w", err)
	}
	repositories, err := useCase.dependencies.GitHub.ListInstallationRepos(ctx, token.Token)
	if err != nil {
		return Result{}, fmt.Errorf("failed to list repositories: %w", err)
	}
	if len(repositories) == 0 {
		return Result{}, fmt.Errorf("no repositories accessible to this installation — grant repository access in GitHub App settings")
	}
	selectedRepositories, err := resolveRepositories(input, repositories, interaction)
	if err != nil {
		return Result{}, err
	}
	interaction.PreparingConfiguration()

	stored, err := useCase.dependencies.Store.Load(input.IdentitiesPath, input.PolicyPath)
	if err != nil {
		return Result{}, fmt.Errorf("load governance configuration: %w", err)
	}
	identities, err := useCase.loadIdentities(input.IdentitiesPath, stored)
	if err != nil {
		return Result{}, err
	}
	identity := identitiesFor(input, installationID).Agents[input.AgentName]
	identities.Agents[input.AgentName] = identity
	policyFile, err := useCase.loadPolicy(input.PolicyPath, identities, stored)
	if err != nil {
		return Result{}, err
	}
	if err := configurePolicy(policyFile, input.AgentName, installationID, selectedRepositories); err != nil {
		return Result{}, err
	}
	if err := useCase.dependencies.ValidatePolicy(policyFile, identities); err != nil {
		return Result{}, fmt.Errorf("refusing to write invalid policy to %s: %w", input.PolicyPath, err)
	}
	if err := useCase.dependencies.Store.Publish(input.IdentitiesPath, identities, input.PolicyPath, policyFile); err != nil {
		return Result{}, fmt.Errorf("publish governance configuration: %w", err)
	}
	return Result{AgentName: input.AgentName, RepositoryCount: len(selectedRepositories), IdentitiesPath: input.IdentitiesPath, PolicyPath: input.PolicyPath}, nil
}

func (useCase *UseCase) validateDependencies(interaction Interaction) error {
	dependencies := useCase.dependencies
	if dependencies.GitHub == nil || dependencies.NewSigner == nil || dependencies.ValidatePolicy == nil || dependencies.Store == nil || interaction == nil {
		return fmt.Errorf("onboarding dependencies are not configured")
	}
	return nil
}

func (useCase *UseCase) resolveInstallation(ctx context.Context, jwt string, input Input, interaction Interaction) (int64, error) {
	if input.InstallationID != 0 {
		interaction.UsingInstallation(input.InstallationID)
		return input.InstallationID, nil
	}
	interaction.QueryingInstallations()
	installations, err := useCase.dependencies.GitHub.ListInstallations(ctx, jwt)
	if err != nil {
		return 0, fmt.Errorf("failed to list installations: %w — verify your App ID and PEM key are correct", err)
	}
	if len(installations) == 0 {
		return 0, fmt.Errorf("no installations found for this GitHub App — install it on at least one account first")
	}
	if len(installations) == 1 {
		interaction.FoundInstallation(installations[0])
		return installations[0].ID, nil
	}
	if input.NonInteractive {
		return 0, fmt.Errorf("multiple installations found; pass --installation-id to select one in non-interactive mode")
	}
	selection, err := interaction.ChooseInstallation(installations)
	if err != nil {
		return 0, err
	}
	index, err := strconv.Atoi(strings.TrimSpace(selection))
	if err != nil || index < 1 || index > len(installations) {
		return 0, fmt.Errorf("invalid selection: %s", selection)
	}
	return installations[index-1].ID, nil
}

func resolveRepositories(input Input, repositories []githubcontract.Repository, interaction Interaction) ([]string, error) {
	selection := strings.TrimSpace(input.RepoSelection)
	byIndex := false
	if selection == "" {
		if input.NonInteractive {
			return nil, fmt.Errorf("--repos is required in non-interactive mode (comma-separated full names or 'all')")
		}
		var err error
		selection, err = interaction.ChooseRepositories(repositories)
		if err != nil {
			return nil, err
		}
		byIndex = true
	}
	if strings.EqualFold(strings.TrimSpace(selection), "all") {
		selected := make([]string, 0, len(repositories))
		for _, repository := range repositories {
			selected = append(selected, repository.FullName)
		}
		return selected, nil
	}
	selected := make([]string, 0, len(repositories))
	for _, part := range strings.Split(selection, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if byIndex {
			index, err := strconv.Atoi(part)
			if err != nil || index < 1 || index > len(repositories) {
				return nil, fmt.Errorf("invalid repo selection: %s", part)
			}
			selected = append(selected, repositories[index-1].FullName)
			continue
		}
		if !repositoryKnown(part, repositories) {
			return nil, fmt.Errorf("repo %q is not accessible to this installation", part)
		}
		selected = append(selected, part)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no repositories selected")
	}
	return selected, nil
}

func repositoryKnown(name string, repositories []githubcontract.Repository) bool {
	for _, repository := range repositories {
		if repository.FullName == name {
			return true
		}
	}
	return false
}

func identitiesFor(input Input, installationID int64) *identity.IdentitiesFile {
	agentIdentity := identity.AgentIdentity{GitName: input.GitName, GitEmail: input.GitEmail, GithubHost: "github.com", AppID: input.AppID, AppKey: input.PEMPath, Tool: defaultToolForAgent(input.AgentName)}
	if installationID != 0 {
		agentIdentity.InstallationID = &installationID
	}
	return &identity.IdentitiesFile{SchemaVersion: schema.IdentitiesSchemaV2, Agents: map[string]identity.AgentIdentity{input.AgentName: agentIdentity}}
}

func defaultToolForAgent(agentName string) string {
	switch agentName {
	case "claude":
		return "claude-code"
	case "codex":
		return "codex"
	default:
		return ""
	}
}

func (useCase *UseCase) loadIdentities(path string, stored StoredGovernance) (*identity.IdentitiesFile, error) {
	if stored.IdentitiesError == nil {
		return stored.Identities, nil
	}
	if errors.Is(stored.IdentitiesError, os.ErrNotExist) {
		return &identity.IdentitiesFile{SchemaVersion: schema.IdentitiesSchemaV2, Agents: make(map[string]identity.AgentIdentity)}, nil
	}
	return nil, fmt.Errorf("existing identities file is invalid: %w — fix or remove %s before running setup", stored.IdentitiesError, path)
}

func (useCase *UseCase) loadPolicy(policyPath string, identities *identity.IdentitiesFile, stored StoredGovernance) (*policy.PolicyFile, error) {
	if errors.Is(stored.PolicyError, os.ErrNotExist) {
		return policy.GenerateDefault(identities), nil
	}
	if stored.PolicyError != nil {
		return nil, fmt.Errorf("existing policy file %s is invalid: %w; fix or remove it before running setup", policyPath, stored.PolicyError)
	}
	if err := useCase.dependencies.ValidatePolicy(stored.Policy, identities); err != nil {
		return nil, fmt.Errorf("existing policy file %s failed validation: %w; fix or remove it before running setup", policyPath, err)
	}
	return stored.Policy, nil
}

func configurePolicy(policyFile *policy.PolicyFile, agentName string, installationID int64, repositories []string) error {
	githubCapability, err := githubSetupCapability()
	if err != nil {
		return err
	}
	resources := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		resource, err := capabilities.ResourceURI(githubCapability.Provider, "repo", repository)
		if err != nil {
			return err
		}
		resources = append(resources, resource.URI)
	}
	providerSection, err := json.Marshal(githubcontract.PolicySection{InstallationID: installationID, DefaultPermissions: policy.DefaultPermissions()})
	if err != nil {
		return fmt.Errorf("encode setup provider section: %w", err)
	}
	policyFile.Agents[agentName] = policy.AgentPolicy{Resources: resources, Providers: map[string]json.RawMessage{githubCapability.Provider: providerSection}}
	return nil
}

func githubSetupCapability() (capabilities.Entry, error) {
	setupProviders := capabilities.SetupProviders()
	if len(setupProviders) != 1 || setupProviders[0] != "github" {
		return capabilities.Entry{}, fmt.Errorf("setup supports github provider only, got %v", setupProviders)
	}
	entry, ok := capabilities.Find("github")
	if !ok || !entry.Setup {
		return capabilities.Entry{}, fmt.Errorf("github setup capability is not registered")
	}
	if _, ok := entry.ResourceKind("repo"); !ok {
		return capabilities.Entry{}, fmt.Errorf("github setup capability does not register repo resources")
	}
	return entry, nil
}
