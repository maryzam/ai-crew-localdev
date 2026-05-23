package broker

import (
	"context"
	"encoding/json"
	"time"
)

// CredentialProvider is the broker-side abstraction that turns a parsed
// ResourceURI into a freshly minted, short-lived credential. Each
// provider handles exactly one CredentialType (e.g. GitHub App
// installation tokens). The broker selects a provider by matching
// Type() against the incoming CredentialRequest.CredentialType.
//
// Stage 5 introduces the interface and a single GitHub implementation
// (in internal/broker/providers/github). Wiring into the server's
// mint_credential handler lands in a later stage.
type CredentialProvider interface {
	// Type returns the credential type string this provider handles,
	// matching one of the CredentialType* constants in api.go.
	Type() string

	// Mint produces a credential for the given request. The resulting
	// payload is opaque to the broker core and is forwarded verbatim as
	// CredentialResponse.Credential.
	Mint(ctx context.Context, req ProviderMintRequest) (ProviderMintResult, error)
}

// ProviderMintRequest is the input passed by the broker to a provider.
// Resource and Agent are derived from the validated session; Params is
// the credential-type-specific payload from the wire request;
// ProviderConfig carries per-agent provider configuration from policy
// (e.g. GitHub installation_id and default_permissions).
type ProviderMintRequest struct {
	Resource ResourceURI
	Params   json.RawMessage
	Agent    string

	// ProviderConfig is provider-specific. Stage 5 uses an empty
	// interface here; stage 9 narrows it once the wiring lands.
	ProviderConfig any
}

// ProviderMintResult is what the provider returns to the broker. The
// broker forwards Credential verbatim to the client and uses ExpiresAt
// for the response and any audit metadata.
type ProviderMintResult struct {
	Credential json.RawMessage
	ExpiresAt  time.Time
}
