package port

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
)

type telemetryOnlyProvider struct{}

func (telemetryOnlyProvider) URIProvider() string                    { return "telemetry" }
func (telemetryOnlyProvider) ValidateResource(api.ResourceURI) error { return nil }
func (telemetryOnlyProvider) ParseConfig(string, json.RawMessage) (any, error) {
	return struct{}{}, nil
}
func (telemetryOnlyProvider) PublishTelemetry(context.Context, ProviderTelemetryRequest) error {
	return nil
}

func TestTelemetryEgressDoesNotRequireCredentialMinting(t *testing.T) {
	var provider Provider = telemetryOnlyProvider{}
	if _, ok := provider.(TelemetryProvider); !ok {
		t.Fatal("telemetry capability not implemented")
	}
	if _, ok := provider.(CredentialProvider); ok {
		t.Fatal("telemetry-only provider unexpectedly implements credential minting")
	}
}
