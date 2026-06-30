package brokerport

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
)

type CredentialProvider interface {
	Type() string
	URIProvider() string
	ValidateResource(uri brokerapi.ResourceURI) error
	ParseConfig(agent string, section json.RawMessage) (any, error)
	PrepareMint(params json.RawMessage, config any) (cacheKey string, err error)
	Mint(ctx context.Context, req ProviderMintRequest) (ProviderMintResult, error)
}

type ProviderMintRequest struct {
	Resource brokerapi.ResourceURI
	Params   json.RawMessage
	Agent    string
	Config   any
}

type ProviderMintResult struct {
	Credential json.RawMessage
	ExpiresAt  time.Time
}
