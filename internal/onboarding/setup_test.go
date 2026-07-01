package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
)

type fakeGitHub struct {
	installations         []githubcontract.Installation
	repositories          []githubcontract.Repository
	installationListCalls int
	mintedInstallation    int64
}

func (github *fakeGitHub) ListInstallations(context.Context, string) ([]githubcontract.Installation, error) {
	github.installationListCalls++
	return github.installations, nil
}

func (github *fakeGitHub) MintInstallationToken(_ context.Context, _ string, installationID int64, _ string, permissions map[string]string) (*githubcontract.InstallationToken, error) {
	if !reflect.DeepEqual(permissions, map[string]string{"metadata": "read"}) {
		return nil, errors.New("unexpected permissions")
	}
	github.mintedInstallation = installationID
	return &githubcontract.InstallationToken{Token: "token"}, nil
}

func (github *fakeGitHub) ListInstallationRepos(context.Context, string) ([]githubcontract.Repository, error) {
	return github.repositories, nil
}

type fakeSigner struct{}

func (fakeSigner) SignJWT(appID string) (string, error) {
	if appID == "" {
		return "", errors.New("missing app ID")
	}
	return "jwt", nil
}

type memoryStore struct {
	identities      *identity.IdentitiesFile
	identitiesError error
	policy          *policy.PolicyFile
	policyError     error
	published       bool
}

func (store *memoryStore) Load(string, string) (StoredGovernance, error) {
	return StoredGovernance{Identities: store.identities, Policy: store.policy, IdentitiesError: store.identitiesError, PolicyError: store.policyError}, nil
}

func (store *memoryStore) Publish(_ string, identities *identity.IdentitiesFile, _ string, policyFile *policy.PolicyFile) error {
	store.identities = identities
	store.policy = policyFile
	store.published = true
	return nil
}

type fakeInteraction struct {
	installationSelection string
	repositorySelection   string
	chooseInstallations   int
	chooseRepositories    int
}

func (interaction *fakeInteraction) UsingInstallation(int64)                       {}
func (interaction *fakeInteraction) QueryingInstallations()                        {}
func (interaction *fakeInteraction) FoundInstallation(githubcontract.Installation) {}
func (interaction *fakeInteraction) ListingRepositories()                          {}
func (interaction *fakeInteraction) PreparingConfiguration()                       {}

func (interaction *fakeInteraction) ChooseInstallation([]githubcontract.Installation) (string, error) {
	interaction.chooseInstallations++
	return interaction.installationSelection, nil
}

func (interaction *fakeInteraction) ChooseRepositories([]githubcontract.Repository) (string, error) {
	interaction.chooseRepositories++
	return interaction.repositorySelection, nil
}

func TestRunDiscoversBuildsValidatesAndPublishes(t *testing.T) {
	github := &fakeGitHub{
		installations: []githubcontract.Installation{installation(10, "first"), installation(20, "second")},
		repositories:  []githubcontract.Repository{{FullName: "org/one"}, {FullName: "org/two", Private: true}},
	}
	store := &memoryStore{identitiesError: os.ErrNotExist, policyError: os.ErrNotExist}
	interaction := &fakeInteraction{installationSelection: "2", repositorySelection: "1,2"}
	validated := false
	useCase := New(Dependencies{
		GitHub: github,
		NewSigner: func(identities *identity.IdentitiesFile) (Signer, error) {
			if identities.Agents["codex"].AppKey != "/keys/app.pem" {
				t.Fatal("temporary identity does not contain the selected PEM")
			}
			return fakeSigner{}, nil
		},
		ValidatePolicy: func(policyFile *policy.PolicyFile, identities *identity.IdentitiesFile) error {
			validated = true
			if identities.Agents["codex"].InstallationID == nil || *identities.Agents["codex"].InstallationID != 20 {
				t.Fatal("identity installation was not resolved")
			}
			if !reflect.DeepEqual(policyFile.Agents["codex"].Resources, []string{"github:repo:org/one", "github:repo:org/two"}) {
				t.Fatalf("resources = %#v", policyFile.Agents["codex"].Resources)
			}
			return nil
		},
		Store: store,
	})

	result, err := useCase.Run(context.Background(), input(), interaction)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !validated || !store.published {
		t.Fatalf("validated = %t, published = %t", validated, store.published)
	}
	if github.mintedInstallation != 20 || interaction.chooseInstallations != 1 || interaction.chooseRepositories != 1 {
		t.Fatalf("minted installation = %d, installation prompts = %d, repository prompts = %d", github.mintedInstallation, interaction.chooseInstallations, interaction.chooseRepositories)
	}
	if result.AgentName != "codex" || result.RepositoryCount != 2 || result.IdentitiesPath != "/config/identities.json" || result.PolicyPath != "/config/policy.json" {
		t.Fatalf("result = %#v", result)
	}
	var section struct {
		InstallationID int64 `json:"installation_id"`
	}
	if err := json.Unmarshal(store.policy.Agents["codex"].Providers["github"], &section); err != nil || section.InstallationID != 20 {
		t.Fatalf("github section = %#v, error = %v", section, err)
	}
}

func TestRunUsesExplicitNonInteractiveSelections(t *testing.T) {
	github := &fakeGitHub{repositories: []githubcontract.Repository{{FullName: "org/one"}}}
	store := &memoryStore{identitiesError: os.ErrNotExist, policyError: os.ErrNotExist}
	interaction := &fakeInteraction{}
	useCase := newTestUseCase(github, store)
	request := input()
	request.InstallationID = 44
	request.RepoSelection = "org/one"
	request.NonInteractive = true

	if _, err := useCase.Run(context.Background(), request, interaction); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if github.installationListCalls != 0 || github.mintedInstallation != 44 || interaction.chooseInstallations != 0 || interaction.chooseRepositories != 0 {
		t.Fatalf("unexpected discovery or prompt calls: %#v %#v", github, interaction)
	}
}

func TestRunRejectsUnknownRepositoryBeforePublication(t *testing.T) {
	github := &fakeGitHub{installations: []githubcontract.Installation{installation(10, "org")}, repositories: []githubcontract.Repository{{FullName: "org/known"}}}
	store := &memoryStore{identitiesError: os.ErrNotExist, policyError: os.ErrNotExist}
	useCase := newTestUseCase(github, store)
	request := input()
	request.RepoSelection = "org/unknown"

	_, err := useCase.Run(context.Background(), request, &fakeInteraction{})
	if err == nil || err.Error() != `repo "org/unknown" is not accessible to this installation` {
		t.Fatalf("Run error = %v", err)
	}
	if store.published {
		t.Fatal("invalid repository selection was published")
	}
}

func TestRunRejectsInvalidExistingPolicyBeforePublication(t *testing.T) {
	github := &fakeGitHub{repositories: []githubcontract.Repository{{FullName: "org/one"}}}
	store := &memoryStore{
		identities: &identity.IdentitiesFile{SchemaVersion: "ai-agent-identities/v2", Agents: map[string]identity.AgentIdentity{"existing": {AppID: "1"}}},
		policy:     &policy.PolicyFile{SchemaVersion: "invalid", Agents: map[string]policy.AgentPolicy{}},
	}
	useCase := New(Dependencies{
		GitHub:    github,
		NewSigner: func(*identity.IdentitiesFile) (Signer, error) { return fakeSigner{}, nil },
		ValidatePolicy: func(*policy.PolicyFile, *identity.IdentitiesFile) error {
			return errors.New("invalid schema")
		},
		Store: store,
	})
	request := input()
	request.InstallationID = 10
	request.RepoSelection = "all"

	_, err := useCase.Run(context.Background(), request, &fakeInteraction{})
	if err == nil || err.Error() != "existing policy file /config/policy.json failed validation: invalid schema; fix or remove it before running setup" {
		t.Fatalf("Run error = %v", err)
	}
	if store.published {
		t.Fatal("invalid existing policy was published")
	}
}

func newTestUseCase(github GitHub, store GovernanceStore) *UseCase {
	return New(Dependencies{
		GitHub:         github,
		NewSigner:      func(*identity.IdentitiesFile) (Signer, error) { return fakeSigner{}, nil },
		ValidatePolicy: func(*policy.PolicyFile, *identity.IdentitiesFile) error { return nil },
		Store:          store,
	})
}

func input() Input {
	return Input{AgentName: "codex", AppID: "123", PEMPath: "/keys/app.pem", GitName: "Codex", GitEmail: "codex@example.com", IdentitiesPath: "/config/identities.json", PolicyPath: "/config/policy.json"}
}

func installation(id int64, login string) githubcontract.Installation {
	return githubcontract.Installation{ID: id, Account: struct {
		Login string `json:"login"`
	}{Login: login}}
}
