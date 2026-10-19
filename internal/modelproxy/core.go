package modelproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
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

type modelSource interface {
	Resolve(context.Context) (modelCredential, error)
	Invalidate(string)
}

type modelProfile struct {
	routes        map[route]bool
	allowAPIKey   bool
	allowDualAuth bool
	authErrors    map[string]bool
	sessionHeader string
	prepare       func(http.Header, modelCredential)
	loginHint     string
}

type modelProxy struct {
	upstream       *url.URL
	placeholder    string
	recorder       Recorder
	transport      http.RoundTripper
	profile        modelProfile
	source         modelSource
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
	ev := Event{Method: auditMethod(r.Method), Path: auditPath(p.profile.routes, r.URL.Path), CredentialVia: credentialVia(r.Header), SessionID: safeSessionID(r.Header.Get(p.profile.sessionHeader))}
	if p.evidenceBroken.Load() {
		refuse(w, http.StatusServiceUnavailable, reasonEvidence)
		return
	}
	if reason := p.refusal(r); reason != "" {
		p.reject(w, ev, http.StatusForbidden, reason)
		return
	}
	credential, err := p.source.Resolve(r.Context())
	if err != nil || credential.secret == "" || strings.Contains(credential.secret, p.placeholder) {
		p.reject(w, ev, http.StatusServiceUnavailable, p.profile.loginHint)
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
			if p.profile.prepare != nil {
				p.profile.prepare(pr.Out.Header, credential)
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
			if resp.StatusCode == http.StatusUnauthorized {
				confirmed, err := confirmedAuthError(resp, p.profile.authErrors)
				if err != nil {
					return err
				}
				if confirmed {
					if !p.record(Event{Decision: Invalidated, Method: ev.Method, Path: ev.Path, SessionID: ev.SessionID, Status: resp.StatusCode}) {
						return errors.New(reasonEvidence)
					}
					p.source.Invalidate(credential.secret)
				}
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

func confirmedAuthError(resp *http.Response, authErrors map[string]bool) (bool, error) {
	for _, value := range resp.Header.Values("Www-Authenticate") {
		if len(value) > 7 && strings.EqualFold(value[:7], "Bearer ") {
			_, challenge, err := mime.ParseMediaType("application/x-auth; " + strings.ReplaceAll(value[7:], ",", ";"))
			if err == nil && challenge["error"] == "invalid_token" {
				return true, nil
			}
		}
	}
	const limit = 16 << 10
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return false, err
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(data), resp.Body), resp.Body}
	if len(data) > limit {
		return false, nil
	}
	var payload struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return false, nil
	}
	return authErrors[payload.Error.Type] || authErrors[payload.Error.Code], nil
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
	if !p.profile.routes[route{r.Method, r.URL.Path}] || r.URL.RawPath != "" || r.URL.EscapedPath() != r.URL.Path || r.URL.Fragment != "" {
		return reasonRoute
	}
	for _, name := range blockedCredentialHeaders {
		if len(r.Header.Values(name)) != 0 {
			return reasonCredential
		}
	}
	authorizations := r.Header.Values("Authorization")
	apiKeys := r.Header.Values("X-Api-Key")
	if len(authorizations) > 1 || len(apiKeys) > 1 || len(authorizations)+len(apiKeys) == 0 || !p.profile.allowAPIKey && len(apiKeys) != 0 || !p.profile.allowDualAuth && len(authorizations)+len(apiKeys) != 1 {
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
	if len(value) > 128 || strings.IndexFunc(value, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return false
		default:
			return true
		}
	}) >= 0 {
		return ""
	}
	return value
}
