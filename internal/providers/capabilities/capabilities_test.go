package capabilities

import "testing"

func TestRegistryEntriesAreComplete(t *testing.T) {
	entries := Registry()
	if len(entries) != 2 {
		t.Fatalf("registry entries = %d, want github and langfuse", len(entries))
	}
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.Provider == "" {
			t.Fatal("provider name must not be empty")
		}
		if _, exists := seen[entry.Provider]; exists {
			t.Fatalf("duplicate provider %q", entry.Provider)
		}
		seen[entry.Provider] = struct{}{}
		if len(entry.ResourceKinds) == 0 {
			t.Fatalf("provider %q has no resource kinds", entry.Provider)
		}
		kinds := map[string]struct{}{}
		telemetryKinds := 0
		for _, kind := range entry.ResourceKinds {
			if kind.Kind == "" {
				t.Fatalf("provider %q has empty resource kind", entry.Provider)
			}
			if _, exists := kinds[kind.Kind]; exists {
				t.Fatalf("provider %q has duplicate resource kind %q", entry.Provider, kind.Kind)
			}
			kinds[kind.Kind] = struct{}{}
			if kind.validateResource == nil {
				t.Fatalf("provider %q kind %q has no resource validator", entry.Provider, kind.Kind)
			}
			if kind.TelemetryEgress {
				telemetryKinds++
			}
		}
		if entry.InterceptionProfile.Provider != entry.Provider {
			t.Fatalf("provider %q interception profile = %q", entry.Provider, entry.InterceptionProfile.Provider)
		}
		if entry.BrokerProvider && !entry.Readiness {
			t.Fatalf("provider %q has broker capability without readiness declaration", entry.Provider)
		}
		if entry.TelemetryEgress && telemetryKinds == 0 {
			t.Fatalf("provider %q has telemetry capability without a telemetry resource kind", entry.Provider)
		}
	}
	github, ok := Find("github")
	if !ok {
		t.Fatal("github provider missing")
	}
	if !github.Setup || !github.CredentialMinting {
		t.Fatalf("github setup/credential declarations = setup %v credential %v", github.Setup, github.CredentialMinting)
	}
	langfuse, ok := Find("langfuse")
	if !ok {
		t.Fatal("langfuse provider missing")
	}
	if !langfuse.TelemetryEgress {
		t.Fatal("langfuse telemetry egress declaration missing")
	}
}

func TestRegistryResourceValidation(t *testing.T) {
	for _, uri := range []string{
		"github:repo:example-org/example-repo",
		"langfuse:project:managed-runs",
	} {
		if _, err := ParseResourceURI(uri); err != nil {
			t.Fatalf("ParseResourceURI(%q): %v", uri, err)
		}
	}
	for _, uri := range []string{
		"github:repo:missing-slash",
		"github:project:example-org/example-repo",
		"langfuse:repo:managed-runs",
		"unknown:repo:example-org/example-repo",
	} {
		if _, err := ParseResourceURI(uri); err == nil {
			t.Fatalf("ParseResourceURI(%q) succeeded", uri)
		}
	}
}

func TestObservabilitySinkMustSupportTelemetry(t *testing.T) {
	if _, err := ObservabilitySink("langfuse:project:managed-runs"); err != nil {
		t.Fatalf("ObservabilitySink(langfuse): %v", err)
	}
	if _, err := ObservabilitySink("github:repo:example-org/example-repo"); err == nil {
		t.Fatal("github repo accepted as telemetry sink")
	}
}

func TestCommandProjectionComesFromInterceptionProfiles(t *testing.T) {
	provider, ok := ProviderForCommand("gh")
	if !ok || provider != "github" {
		t.Fatalf("ProviderForCommand(gh) = %q, %v", provider, ok)
	}
	for _, command := range Commands() {
		if command == "" {
			t.Fatal("empty command in capability projection")
		}
	}
}
