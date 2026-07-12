package catalog

import (
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/providers/capabilities"
)

func TestProviderConstructorsFollowCapabilityRegistry(t *testing.T) {
	providers, err := Providers(&identity.IdentitiesFile{}, "")
	if err != nil {
		t.Fatalf("construct providers: %v", err)
	}
	validators, err := Validators(&identity.IdentitiesFile{})
	if err != nil {
		t.Fatalf("construct validators: %v", err)
	}
	want := providerSet(capabilities.BrokerProviders())

	assertProviderSet(t, "providers", providers, want)
	assertProviderSet(t, "validators", validators, want)
}

func TestConstructorTableCoversBrokerCapabilities(t *testing.T) {
	for _, provider := range capabilities.BrokerProviders() {
		constructor, ok := constructors[provider]
		if !ok {
			t.Fatalf("missing constructor for %q", provider)
		}
		if constructor.broker == nil || constructor.validator == nil {
			t.Fatalf("incomplete constructor for %q", provider)
		}
	}
}

func providerSet(names []string) map[string]struct{} {
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result
}

func assertProviderSet[T interface{ URIProvider() string }](t *testing.T, label string, providers []T, want map[string]struct{}) {
	t.Helper()
	got := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		got[provider.URIProvider()] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Fatalf("%s missing provider %q", label, name)
		}
	}
}
