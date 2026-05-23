// Package github implements the broker's CredentialProvider for GitHub
// App installation tokens. It wraps the existing broker.GitHubClient
// and broker.Signer; the broker depends on this package, never the
// other way around, so there is no import cycle.
package github

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
)

// Config is the per-agent provider configuration extracted from policy
// and passed to Mint via ProviderMintRequest.ProviderConfig. The broker
// is responsible for populating this from the policy file before
// dispatch; the provider treats it as opaque input.
type Config struct {
	InstallationID     int64             `json:"installation_id"`
	AppID              string            `json:"app_id"`
	DefaultPermissions map[string]string `json:"default_permissions,omitempty"`
}

// Provider mints GitHub App installation access tokens.
type Provider struct {
	client *broker.GitHubClient
	signer *broker.Signer
}

// New returns a Provider that uses the given GitHubClient and Signer.
// Both are owned by the broker; the provider does not close them.
func New(client *broker.GitHubClient, signer *broker.Signer) *Provider {
	return &Provider{client: client, signer: signer}
}

// Type implements CredentialProvider.
func (p *Provider) Type() string {
	return broker.CredentialTypeGitHubAppInstallation
}

// Mint implements CredentialProvider. It expects ProviderConfig to be a
// *Config (or Config), Params to be a JSON-encoded
// broker.GitHubAppInstallationParams, and Resource to be a parsed
// github:repo:<owner/name> URI.
func (p *Provider) Mint(ctx context.Context, req broker.ProviderMintRequest) (broker.ProviderMintResult, error) {
	cfg, err := extractConfig(req.ProviderConfig)
	if err != nil {
		return broker.ProviderMintResult{}, err
	}

	if req.Resource.Provider != "github" || req.Resource.Kind != "repo" {
		return broker.ProviderMintResult{}, fmt.Errorf("github provider: unsupported resource %s:%s",
			req.Resource.Provider, req.Resource.Kind)
	}

	permissions := cfg.DefaultPermissions
	if len(req.Params) > 0 {
		var params broker.GitHubAppInstallationParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return broker.ProviderMintResult{}, fmt.Errorf("github provider: parse params: %w", err)
		}
		if len(params.Permissions) > 0 {
			permissions = params.Permissions
		}
	}

	jwt, err := p.signer.SignJWT(cfg.AppID)
	if err != nil {
		return broker.ProviderMintResult{}, fmt.Errorf("github provider: sign JWT: %w", err)
	}

	tok, err := p.client.MintInstallationToken(
		ctx, jwt, cfg.InstallationID, req.Resource.Identifier, permissions,
	)
	if err != nil {
		return broker.ProviderMintResult{}, fmt.Errorf("github provider: mint token: %w", err)
	}

	cred := broker.GitHubAppInstallationCredential{Token: tok.Token}
	payload, err := json.Marshal(cred)
	if err != nil {
		return broker.ProviderMintResult{}, fmt.Errorf("github provider: marshal credential: %w", err)
	}

	return broker.ProviderMintResult{
		Credential: payload,
		ExpiresAt:  tok.ExpiresAt,
	}, nil
}

// extractConfig accepts either Config or *Config from the broker.
// Using a typed extractor keeps the wire seam at exactly one place;
// stage 9 will tighten ProviderMintRequest.ProviderConfig to a concrete
// type and this helper goes away.
func extractConfig(raw any) (Config, error) {
	switch c := raw.(type) {
	case Config:
		return c, nil
	case *Config:
		if c == nil {
			return Config{}, fmt.Errorf("github provider: nil ProviderConfig")
		}
		return *c, nil
	default:
		return Config{}, fmt.Errorf("github provider: unexpected ProviderConfig type %T", raw)
	}
}
