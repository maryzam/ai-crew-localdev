package modelproxy

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	codexPrefix        = "/backend-api/codex"
	reasonCodexAuth    = "host Codex login is unavailable"
	reasonCodexHeaders = "credential headers are not the run placeholder"
)

type CodexConfig struct {
	Upstream    *url.URL
	Placeholder string
	Auth        CodexCredentialSource
	Recorder    Recorder
	Transport   http.RoundTripper
}

type CodexProxy struct {
	upstream       *url.URL
	placeholder    string
	auth           CodexCredentialSource
	recorder       Recorder
	transport      http.RoundTripper
	evidenceBroken atomic.Bool
}

func NewCodex(cfg CodexConfig) (*CodexProxy, error) {
	if cfg.Upstream == nil || cfg.Upstream.Scheme != "https" || cfg.Upstream.Host == "" || cfg.Upstream.Path != "" || cfg.Upstream.RawQuery != "" || cfg.Upstream.Fragment != "" || cfg.Upstream.User != nil {
		return nil, errors.New("codex upstream must be an https origin")
	}
	if len(cfg.Placeholder) < 32 || strings.ContainsAny(cfg.Placeholder, " \t\r\n") || cfg.Auth == nil || cfg.Recorder == nil {
		return nil, errors.New("codex proxy needs a placeholder, host credential source, and evidence recorder")
	}
	return &CodexProxy{upstream: cfg.Upstream, placeholder: cfg.Placeholder, auth: cfg.Auth, recorder: cfg.Recorder, transport: cfg.Transport}, nil
}

func (p *CodexProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ev := Event{Method: codexAuditMethod(r.Method), Path: codexAuditPath(r.URL.Path), CredentialVia: codexCredentialVia(r.Header)}
	if p.evidenceBroken.Load() {
		refuse(w, http.StatusServiceUnavailable, reasonEvidence)
		return
	}
	if reason := p.refusal(r); reason != "" {
		p.reject(w, ev, http.StatusForbidden, reason)
		return
	}
	credential, err := p.auth.Resolve(r.Context())
	if err != nil || credential.accessToken == "" || credential.accountID == "" || strings.Contains(credential.accessToken, p.placeholder) {
		p.reject(w, ev, http.StatusServiceUnavailable, reasonCodexAuth)
		return
	}
	ev.Decision = Forwarded
	if !p.record(ev) {
		refuse(w, http.StatusServiceUnavailable, reasonEvidence)
		return
	}
	start := time.Now()
	out := &outcome{}
	forward := &httputil.ReverseProxy{
		FlushInterval: -1,
		Transport:     p.transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(p.upstream)
			for _, header := range []string{"X-Api-Key", "X-OpenAI-API-Key", "Cookie", "Proxy-Authorization", "X-Gateway-Key", "OpenAI-Organization", "OpenAI-Project"} {
				pr.Out.Header.Del(header)
			}
			pr.Out.Header.Set("Accept-Encoding", "identity")
			pr.Out.Header.Set("Authorization", "Bearer "+credential.accessToken)
			pr.Out.Header.Set("ChatGPT-Account-ID", credential.accountID)
		},
		ModifyResponse: func(resp *http.Response) error {
			if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
				return errors.New("encoded model response cannot be inspected")
			}
			resp.Header.Del("Content-Length")
			resp.ContentLength = -1
			if resp.StatusCode == http.StatusUnauthorized {
				p.auth.Invalidate()
			}
			resp.Body = &echoScanner{ReadCloser: resp.Body, needle: []byte(credential.accessToken), outcome: out}
			return nil
		},
	}
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	forward.ServeHTTP(sw, r)
	ev.Decision = Completed
	ev.Status = sw.status
	ev.DurationMS = time.Since(start).Milliseconds()
	ev.ResponseBytes = out.bytes
	ev.CredentialEchoed = out.echoed
	p.record(ev)
}

func (p *CodexProxy) refusal(r *http.Request) string {
	if (r.Method != http.MethodPost || r.URL.Path != codexPrefix+"/responses") && (r.Method != http.MethodGet || r.URL.Path != codexPrefix+"/models") {
		return reasonRoute
	}
	if r.URL.RawPath != "" || r.URL.EscapedPath() != r.URL.Path || r.URL.Fragment != "" {
		return reasonRoute
	}
	if len(r.Header.Values("Authorization")) != 1 || r.Header.Get("Authorization") != "Bearer "+p.placeholder {
		return reasonCredential
	}
	for _, header := range []string{"X-Api-Key", "X-OpenAI-API-Key", "Cookie", "Proxy-Authorization", "X-Gateway-Key", "OpenAI-Organization", "OpenAI-Project", "ChatGPT-Account-ID"} {
		if len(r.Header.Values(header)) != 0 {
			return reasonCodexHeaders
		}
	}
	return ""
}

func (p *CodexProxy) reject(w http.ResponseWriter, ev Event, status int, reason string) {
	ev.Decision, ev.Reason = Refused, reason
	if !p.record(ev) {
		refuse(w, http.StatusServiceUnavailable, reasonEvidence)
		return
	}
	refuse(w, status, reason)
}

func (p *CodexProxy) record(ev Event) bool {
	if err := p.recorder.Record(ev); err != nil {
		p.evidenceBroken.Store(true)
		return false
	}
	return true
}

func codexCredentialVia(h http.Header) []string {
	via := []string{}
	for _, name := range []string{"Authorization", "X-Api-Key", "ChatGPT-Account-ID", "Cookie"} {
		if len(h.Values(name)) > 0 {
			via = append(via, strings.ToLower(name))
		}
	}
	return via
}

func codexAuditPath(path string) string {
	if path == codexPrefix+"/responses" || path == codexPrefix+"/models" {
		return path
	}
	return "other"
}

func codexAuditMethod(method string) string {
	if method == http.MethodPost || method == http.MethodGet {
		return method
	}
	return "other"
}
