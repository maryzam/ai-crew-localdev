package github

import (
	"fmt"
	"regexp"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
)

var repoSlugPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$`)

func validateResource(uri brokerapi.ResourceURI) error {
	if uri.Provider != uriProvider {
		return fmt.Errorf("github provider: resource provider %q is not %q", uri.Provider, uriProvider)
	}
	if uri.Kind != uriKind {
		return fmt.Errorf("github provider: resource kind %q is not supported (want %q)", uri.Kind, uriKind)
	}
	if !repoSlugPattern.MatchString(uri.Identifier) {
		return fmt.Errorf("github provider: invalid repo identifier %q (want owner/name)", uri.Identifier)
	}
	return nil
}
