package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
)

const (
	credentialType = githubcontract.CredentialType
	uriProvider    = "github"
	uriKind        = "repo"
)

type Provider struct {
	client       *GitHubClient
	signer       *Signer
	resolveAppID func(agent string) string
}

func New(client *GitHubClient, signer *Signer, resolveAppID func(agent string) string) *Provider {
	if resolveAppID == nil {
		resolveAppID = func(string) string { return "" }
	}
	return &Provider{client: client, signer: signer, resolveAppID: resolveAppID}
}

func NewValidator(resolveAppID func(agent string) string) *Provider {
	return New(nil, nil, resolveAppID)
}

func (p *Provider) Type() string        { return credentialType }
func (p *Provider) URIProvider() string { return uriProvider }

func (p *Provider) ValidateResource(uri api.ResourceURI) error {
	return validateResource(uri)
}

func (p *Provider) ParseConfig(agent string, section json.RawMessage) (any, error) {
	return parseConfig(agent, section, p.resolveAppID)
}

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

func (p *Provider) Mint(ctx context.Context, req port.ProviderMintRequest) (port.ProviderMintResult, error) {
	cfg, err := assertConfig(req.Config)
	if err != nil {
		return port.ProviderMintResult{}, err
	}
	if req.Resource.Provider != uriProvider || req.Resource.Kind != uriKind {
		return port.ProviderMintResult{}, fmt.Errorf("github provider: unsupported resource %s:%s",
			req.Resource.Provider, req.Resource.Kind)
	}
	effective, err := effectivePermissions(req.Params, cfg.DefaultPermissions)
	if err != nil {
		return port.ProviderMintResult{}, err
	}

	jwt, err := p.signer.SignJWT(cfg.AppID)
	if err != nil {
		return port.ProviderMintResult{}, fmt.Errorf("github provider: sign JWT: %w", err)
	}

	tok, err := p.client.MintInstallationToken(ctx, jwt, cfg.InstallationID, req.Resource.Identifier, effective)
	if err != nil {
		return port.ProviderMintResult{}, fmt.Errorf("github provider: mint token: %w", err)
	}

	payload, err := json.Marshal(githubcontract.Credential{Token: tok.Token})
	if err != nil {
		return port.ProviderMintResult{}, fmt.Errorf("github provider: marshal credential: %w", err)
	}
	return port.ProviderMintResult{Credential: payload, ExpiresAt: tok.ExpiresAt}, nil
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
	var p githubcontract.Params
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
