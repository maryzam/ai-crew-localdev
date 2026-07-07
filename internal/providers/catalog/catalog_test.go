package catalog

import (
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
)

func TestEveryProviderDeclaresAnInterceptionProfile(t *testing.T) {
	providers, err := Providers(&identity.IdentitiesFile{}, "")
	if err != nil {
		t.Fatalf("construct providers: %v", err)
	}

	profiles := make(map[string]struct{})
	for _, profile := range InterceptionProfiles() {
		if profile.Provider == "" {
			t.Fatal("interception profile with empty provider name")
		}
		if _, dup := profiles[profile.Provider]; dup {
			t.Fatalf("duplicate interception profile for provider %q", profile.Provider)
		}
		profiles[profile.Provider] = struct{}{}
	}

	if len(providers) != len(profiles) {
		t.Fatalf("catalog has %d providers but %d interception profiles", len(providers), len(profiles))
	}
	for _, provider := range providers {
		if _, ok := profiles[provider.URIProvider()]; !ok {
			t.Errorf("provider %q registered without an interception profile", provider.URIProvider())
		}
	}
}
