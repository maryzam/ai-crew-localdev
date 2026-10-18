package modelproxy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

const codexPrefix = "/backend-api/codex"

var codexRoutes = map[route]bool{
	{http.MethodPost, codexPrefix + "/responses"}: true,
	{http.MethodGet, codexPrefix + "/models"}:     true,
}

type CodexConfig struct {
	Upstream    *url.URL
	Placeholder string
	Auth        CodexCredentialSource
	Recorder    Recorder
	Transport   http.RoundTripper
}

type CodexProxy struct {
	*modelProxy
}

func NewCodex(cfg CodexConfig) (*CodexProxy, error) {
	if err := validateModelProxy(cfg.Upstream, cfg.Placeholder, cfg.Recorder); err != nil {
		return nil, err
	}
	if cfg.Auth == nil {
		return nil, errors.New("codex proxy needs a host credential source")
	}
	return &CodexProxy{modelProxy: &modelProxy{
		upstream: cfg.Upstream, placeholder: cfg.Placeholder, recorder: cfg.Recorder, transport: cfg.Transport,
		routes: codexRoutes, provider: "codex", resolve: func(ctx context.Context) (modelCredential, error) {
			credential, err := cfg.Auth.Resolve(ctx)
			return modelCredential{secret: credential.accessToken, accountID: credential.accountID}, err
		}, invalidate: cfg.Auth.Invalidate,
	}}, nil
}
