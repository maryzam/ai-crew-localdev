package modelproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type modelCredential struct {
	secret    string
	accountID string
}

type modelProxy struct {
	upstream       *url.URL
	placeholder    string
	recorder       Recorder
	transport      http.RoundTripper
	routes         map[route]bool
	provider       string
	resolve        func(context.Context) (modelCredential, error)
	invalidate     func()
	evidenceBroken atomic.Bool
}

var blockedCredentialHeaders = []string{
	"Cookie", "Proxy-Authorization", "X-Gateway-Key", "OpenAI-Organization", "OpenAI-Project", "ChatGPT-Account-ID", "X-OpenAI-API-Key",
}

func validateModelProxy(upstream *url.URL, placeholder string, recorder Recorder) error {
	if upstream == nil || upstream.Scheme != "https" || upstream.Host == "" || upstream.Path != "" || upstream.RawQuery != "" || upstream.Fragment != "" || upstream.User != nil {
		return errors.New("model upstream must be an https origin")
	}
	if len(placeholder) < 32 || strings.ContainsAny(placeholder, " \t\r\n") || recorder == nil {
		return errors.New("model proxy needs a long run placeholder and evidence recorder")
	}
	return nil
}

func (p *modelProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ev := Event{Method: auditMethod(r.Method), Path: auditPath(p.routes, r.URL.Path), CredentialVia: credentialVia(r.Header), SessionID: safeSessionID(r.Header.Get(sessionHeader))}
	if p.evidenceBroken.Load() {
		refuse(w, http.StatusServiceUnavailable, reasonEvidence)
		return
	}
	if reason := p.refusal(r); reason != "" {
		p.reject(w, ev, http.StatusForbidden, reason)
		return
	}
	credential, err := p.resolve(r.Context())
	if err != nil || credential.secret == "" || strings.Contains(credential.secret, p.placeholder) || p.provider == "codex" && credential.accountID == "" {
		p.reject(w, ev, http.StatusServiceUnavailable, "host model login is unavailable")
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
			for _, name := range blockedCredentialHeaders {
				pr.Out.Header.Del(name)
			}
			pr.Out.Header.Del("X-Api-Key")
			pr.Out.Header.Set("Accept-Encoding", "identity")
			pr.Out.Header.Set("Authorization", "Bearer "+credential.secret)
			if p.provider == "codex" {
				pr.Out.Header.Set("ChatGPT-Account-ID", credential.accountID)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
				return errors.New("encoded model response cannot be inspected")
			}
			for name, values := range resp.Header {
				if strings.EqualFold(name, "Set-Cookie") || strings.EqualFold(name, "Trailer") {
					resp.Header.Del(name)
					continue
				}
				for i, value := range values {
					values[i] = strings.ReplaceAll(value, credential.secret, "[redacted]")
				}
			}
			resp.Trailer = nil
			resp.Header.Del("Content-Length")
			resp.ContentLength = -1
			if resp.StatusCode == http.StatusUnauthorized && p.invalidate != nil {
				p.invalidate()
			}
			resp.Body = &trailerDropper{ReadCloser: &echoScanner{ReadCloser: resp.Body, needle: []byte(credential.secret), outcome: out}, response: resp}
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

type trailerDropper struct {
	io.ReadCloser
	response *http.Response
}

func (d *trailerDropper) Close() error {
	err := d.ReadCloser.Close()
	d.response.Trailer = nil
	return err
}

func (p *modelProxy) refusal(r *http.Request) string {
	if !p.routes[route{r.Method, r.URL.Path}] || r.URL.RawPath != "" || r.URL.EscapedPath() != r.URL.Path || r.URL.Fragment != "" {
		return reasonRoute
	}
	for _, name := range blockedCredentialHeaders {
		if len(r.Header.Values(name)) != 0 {
			return reasonCredential
		}
	}
	authorizations := r.Header.Values("Authorization")
	apiKeys := r.Header.Values("X-Api-Key")
	if len(authorizations) > 1 || len(apiKeys) > 1 || p.provider == "codex" && (len(authorizations) != 1 || len(apiKeys) != 0) || p.provider == "claude" && len(authorizations)+len(apiKeys) == 0 {
		return reasonCredential
	}
	if len(authorizations) == 1 && authorizations[0] != "Bearer "+p.placeholder || len(apiKeys) == 1 && apiKeys[0] != p.placeholder {
		return reasonCredential
	}
	return ""
}

func (p *modelProxy) reject(w http.ResponseWriter, ev Event, status int, reason string) {
	ev.Decision, ev.Reason = Refused, reason
	if !p.record(ev) {
		refuse(w, http.StatusServiceUnavailable, reasonEvidence)
		return
	}
	refuse(w, status, reason)
}

func (p *modelProxy) record(ev Event) bool {
	if err := p.recorder.Record(ev); err != nil {
		p.evidenceBroken.Store(true)
		return false
	}
	return true
}

func credentialVia(h http.Header) []string {
	via := []string{}
	for _, name := range []string{"Authorization", "X-Api-Key", "X-OpenAI-API-Key", "ChatGPT-Account-ID", "Cookie"} {
		if len(h.Values(name)) > 0 {
			via = append(via, strings.ToLower(name))
		}
	}
	return via
}

func auditPath(routes map[route]bool, path string) string {
	if routes[route{http.MethodGet, path}] || routes[route{http.MethodPost, path}] {
		return path
	}
	return "other"
}

func auditMethod(method string) string {
	if method == http.MethodPost || method == http.MethodGet {
		return method
	}
	return "other"
}

func safeSessionID(value string) string {
	if len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if r < 'a' || r > 'z' {
			if r < 'A' || r > 'Z' {
				if r < '0' || r > '9' {
					if r != '-' && r != '_' && r != '.' {
						return ""
					}
				}
			}
		}
	}
	return value
}
