package port

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
)

type CredentialProvider interface {
	Type() string
	URIProvider() string
	ValidateResource(uri api.ResourceURI) error
	ParseConfig(agent string, section json.RawMessage) (any, error)
	PrepareMint(params json.RawMessage, config any) (cacheKey string, err error)
	Mint(ctx context.Context, req ProviderMintRequest) (ProviderMintResult, error)
}

type ProviderMintRequest struct {
	Resource api.ResourceURI
	Params   json.RawMessage
	Agent    string
	Config   any
}

type ProviderMintResult struct {
	Credential json.RawMessage
	ExpiresAt  time.Time
}
