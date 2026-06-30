package broker

import (
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/brokerport"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

// ValidatePolicy runs the same schema and provider-level checks NewBroker
// performs at startup, without constructing a runtime broker. Callers that
// generate or mutate policy files (e.g. ai-agent setup) use this so they
// never persist a policy the broker would reject on startup.
func ValidatePolicy(p *policy.PolicyFile, providers []brokerport.CredentialProvider) error {
	if result := policy.Validate(p); result.Errors.HasErrors() {
		return fmt.Errorf("policy schema: %s", result.Errors.Error())
	}
	registry, err := newProviderRegistry(providers)
	if err != nil {
		return err
	}
	_, err = registry.validateAndParseConfigs(p)
	return err
}
