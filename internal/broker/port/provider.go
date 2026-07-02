package port

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
)

type Provider interface {
	URIProvider() string
	ValidateResource(uri api.ResourceURI) error
	ParseConfig(agent string, section json.RawMessage) (any, error)
}

type CredentialProvider interface {
	Provider
	Type() string
	PrepareMint(params json.RawMessage, config any) (cacheKey string, err error)
	Mint(ctx context.Context, req ProviderMintRequest) (ProviderMintResult, error)
}

type TelemetryProvider interface {
	Provider
	PublishTelemetry(ctx context.Context, req ProviderTelemetryRequest) error
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

type ProviderTelemetryRequest struct {
	Resource api.ResourceURI
	Agent    string
	Config   any
	Payload  json.RawMessage
}
