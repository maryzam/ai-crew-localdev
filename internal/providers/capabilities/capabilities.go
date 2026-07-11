package capabilities

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/interception"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
	langfusecontract "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse/contract"
)

type Entry struct {
	Provider            string
	ResourceKinds       []ResourceKind
	BrokerProvider      bool
	CredentialMinting   bool
	TelemetryEgress     bool
	InterceptionProfile interception.Profile
	Setup               bool
	Readiness           bool
}

type ResourceKind struct {
	Kind             string
	TelemetryEgress  bool
	validateResource func(string) error
}

type Resource struct {
	URI        string
	Provider   string
	Kind       string
	Identifier string
}

func Registry() []Entry {
	return []Entry{
		{
			Provider:            "github",
			ResourceKinds:       []ResourceKind{{Kind: "repo", validateResource: validateGitHubRepo}},
			BrokerProvider:      true,
			CredentialMinting:   true,
			InterceptionProfile: githubcontract.InterceptionProfile(),
			Setup:               true,
			Readiness:           true,
		},
		{
			Provider:            "langfuse",
			ResourceKinds:       []ResourceKind{{Kind: "project", TelemetryEgress: true, validateResource: validateLangfuseProject}},
			BrokerProvider:      true,
			TelemetryEgress:     true,
			InterceptionProfile: langfusecontract.InterceptionProfile(),
			Readiness:           true,
		},
	}
}

func BrokerProviders() []string {
	var providers []string
	for _, entry := range Registry() {
		if entry.BrokerProvider {
			providers = append(providers, entry.Provider)
		}
	}
	return providers
}

func InterceptionProfiles() []interception.Profile {
	var profiles []interception.Profile
	for _, entry := range Registry() {
		profiles = append(profiles, entry.InterceptionProfile)
	}
	return profiles
}

func Commands() []string {
	var commands []string
	for _, profile := range InterceptionProfiles() {
		commands = append(commands, profile.Commands...)
	}
	return commands
}

func ProviderForCommand(command string) (string, bool) {
	for _, profile := range InterceptionProfiles() {
		for _, candidate := range profile.Commands {
			if candidate == command {
				return profile.Provider, true
			}
		}
	}
	return "", false
}

func ResourceURI(provider string, kind string, identifier string) (Resource, error) {
	entry, ok := Find(provider)
	if !ok {
		return Resource{}, fmt.Errorf("provider %q is not registered", provider)
	}
	resourceKind, ok := entry.ResourceKind(kind)
	if !ok {
		return Resource{}, fmt.Errorf("provider %q does not register resource kind %q", provider, kind)
	}
	if err := resourceKind.Validate(identifier); err != nil {
		return Resource{}, err
	}
	return Resource{URI: provider + ":" + kind + ":" + identifier, Provider: provider, Kind: kind, Identifier: identifier}, nil
}

func ParseResourceURI(uri string) (Resource, error) {
	provider, rest, ok := strings.Cut(uri, ":")
	if !ok || provider == "" {
		return Resource{}, fmt.Errorf("resource URI %q: missing provider", uri)
	}
	kind, identifier, ok := strings.Cut(rest, ":")
	if !ok || kind == "" {
		return Resource{}, fmt.Errorf("resource URI %q: missing kind", uri)
	}
	if identifier == "" {
		return Resource{}, fmt.Errorf("resource URI %q: missing identifier", uri)
	}
	resource, err := ResourceURI(provider, kind, identifier)
	if err != nil {
		return Resource{}, err
	}
	return resource, nil
}

func ObservabilitySink(uri string) (Resource, error) {
	resource, err := ParseResourceURI(uri)
	if err != nil {
		return Resource{}, err
	}
	entry, _ := Find(resource.Provider)
	kind, _ := entry.ResourceKind(resource.Kind)
	if !entry.TelemetryEgress || !kind.TelemetryEgress {
		return Resource{}, fmt.Errorf("resource %q does not support telemetry egress", uri)
	}
	return resource, nil
}

func Find(provider string) (Entry, bool) {
	for _, entry := range Registry() {
		if entry.Provider == provider {
			return entry, true
		}
	}
	return Entry{}, false
}

func (entry Entry) ResourceKind(kind string) (ResourceKind, bool) {
	for _, candidate := range entry.ResourceKinds {
		if candidate.Kind == kind {
			return candidate, true
		}
	}
	return ResourceKind{}, false
}

func (kind ResourceKind) Validate(identifier string) error {
	if kind.validateResource == nil {
		if strings.TrimSpace(identifier) == "" {
			return fmt.Errorf("resource identifier must not be empty")
		}
		return nil
	}
	return kind.validateResource(identifier)
}

var githubRepoPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$`)

func validateGitHubRepo(identifier string) error {
	if !githubRepoPattern.MatchString(identifier) {
		return fmt.Errorf("github repo identifier %q must use owner/name", identifier)
	}
	return nil
}

func validateLangfuseProject(identifier string) error {
	if strings.TrimSpace(identifier) == "" || len(identifier) > 128 {
		return fmt.Errorf("langfuse project identifier is invalid")
	}
	return nil
}
