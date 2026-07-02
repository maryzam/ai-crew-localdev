package core

import (
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
)

func ValidatePolicy(p *policy.PolicyFile, providers []port.Provider) error {
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
