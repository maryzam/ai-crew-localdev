package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
)

const (
	credentialType = broker.CredentialTypeGitHubAppInstallation
	uriProvider    = "github"
	uriKind        = "repo"
)

// Provider mints GitHub App installation access tokens for github:repo:<owner/name>.
type Provider struct {
	client       *broker.GitHubClient
	signer       *broker.Signer
	resolveAppID func(agent string) string
}

// New returns a Provider that mints tokens using the given GitHubClient,
// Signer, and an agent-to-AppID resolver invoked when policy omits app_id.
func New(client *broker.GitHubClient, signer *broker.Signer, resolveAppID func(agent string) string) *Provider {
	if resolveAppID == nil {
		resolveAppID = func(string) string { return "" }
	}
	return &Provider{client: client, signer: signer, resolveAppID: resolveAppID}
}

// NewValidator returns a Provider configured for policy validation only:
// ValidateResource and ParseConfig are safe to call; Mint and PrepareMint
// are not (they require a client and signer).
func NewValidator(resolveAppID func(agent string) string) *Provider {
	return New(nil, nil, resolveAppID)
}

func (p *Provider) Type() string        { return credentialType }
func (p *Provider) URIProvider() string { return uriProvider }

func (p *Provider) ValidateResource(uri broker.ResourceURI) error {
	return validateResource(uri)
}

func (p *Provider) ParseConfig(agent string, section json.RawMessage) (any, error) {
	return parseConfig(agent, section, p.resolveAppID)
}

// PrepareMint validates that the requested permissions are a subset of the
// policy default permissions and returns a stable cache key contribution over
// the effective permissions and the config identity. The config identity
// (installation_id, app_id) ensures cache entries do not survive across
// policy reloads that change which GitHub App or installation mints the token.
func (p *Provider) PrepareMint(params json.RawMessage, config any) (string, error) {
	cfg, err := assertConfig(config)
	if err != nil {
		return "", err
	}
	effective, err := effectivePermissions(params, cfg.DefaultPermissions)
	if err != nil {
		return "", err
	}
	return cacheKeyContribution(cfg, effective), nil
}

func (p *Provider) Mint(ctx context.Context, req broker.ProviderMintRequest) (broker.ProviderMintResult, error) {
	cfg, err := assertConfig(req.Config)
	if err != nil {
		return broker.ProviderMintResult{}, err
	}
	if req.Resource.Provider != uriProvider || req.Resource.Kind != uriKind {
		return broker.ProviderMintResult{}, fmt.Errorf("github provider: unsupported resource %s:%s",
			req.Resource.Provider, req.Resource.Kind)
	}
	effective, err := effectivePermissions(req.Params, cfg.DefaultPermissions)
	if err != nil {
		return broker.ProviderMintResult{}, err
	}

	jwt, err := p.signer.SignJWT(cfg.AppID)
	if err != nil {
		return broker.ProviderMintResult{}, fmt.Errorf("github provider: sign JWT: %w", err)
	}

	tok, err := p.client.MintInstallationToken(ctx, jwt, cfg.InstallationID, req.Resource.Identifier, effective)
	if err != nil {
		return broker.ProviderMintResult{}, fmt.Errorf("github provider: mint token: %w", err)
	}

	payload, err := json.Marshal(broker.GitHubAppInstallationCredential{Token: tok.Token})
	if err != nil {
		return broker.ProviderMintResult{}, fmt.Errorf("github provider: marshal credential: %w", err)
	}
	return broker.ProviderMintResult{Credential: payload, ExpiresAt: tok.ExpiresAt}, nil
}

func assertConfig(raw any) (Config, error) {
	switch c := raw.(type) {
	case Config:
		return c, nil
	case *Config:
		if c == nil {
			return Config{}, fmt.Errorf("github provider: nil config")
		}
		return *c, nil
	default:
		return Config{}, fmt.Errorf("github provider: unexpected config type %T", raw)
	}
}

func effectivePermissions(rawParams json.RawMessage, defaults map[string]string) (map[string]string, error) {
	if len(rawParams) == 0 || string(rawParams) == "null" {
		return defaults, nil
	}
	var p broker.GitHubAppInstallationParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return nil, fmt.Errorf("github provider: parse params: %w", err)
	}
	if len(p.Permissions) == 0 {
		return defaults, nil
	}
	if err := validatePermissionSubset(p.Permissions, defaults); err != nil {
		return nil, err
	}
	return p.Permissions, nil
}

func cacheKeyContribution(cfg Config, perms map[string]string) string {
	parts := []string{
		"install=" + strconv.FormatInt(cfg.InstallationID, 10),
		"app=" + cfg.AppID,
		"perms=" + serializePermissions(perms),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}
