package brokerport

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
)

// CredentialProvider mints credentials for one credential type. Each provider
// owns its policy-section schema, its resource grammar, its mint-request
// validation, and its cache key contribution. The broker is provider-agnostic
// and dispatches by Type().
type CredentialProvider interface {
	Type() string
	URIProvider() string
	ValidateResource(uri brokerapi.ResourceURI) error
	ParseConfig(agent string, section json.RawMessage) (any, error)
	PrepareMint(params json.RawMessage, config any) (cacheKey string, err error)
	Mint(ctx context.Context, req ProviderMintRequest) (ProviderMintResult, error)
}

// ProviderMintRequest is what the broker passes to a provider for a single
// mint operation. Config is the value previously returned by ParseConfig.
type ProviderMintRequest struct {
	Resource brokerapi.ResourceURI
	Params   json.RawMessage
	Agent    string
	Config   any
}

// ProviderMintResult is what the provider returns to the broker. Credential
// is forwarded verbatim to the client; the broker does not inspect its shape.
type ProviderMintResult struct {
	Credential json.RawMessage
	ExpiresAt  time.Time
}
