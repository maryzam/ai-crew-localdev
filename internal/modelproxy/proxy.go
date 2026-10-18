package modelproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type Decision string

const (
	Refused   Decision = "refused"
	Forwarded Decision = "forwarded"
	Completed Decision = "completed"
)

const (
	reasonRoute       = "route is not a model endpoint"
	reasonCredential  = "credential is not the run placeholder"
	reasonEvidence    = "evidence is unavailable"
	sessionHeader     = "X-Claude-Code-Session-Id"
	shouldRetryHeader = "X-Should-Retry"
)

type Event struct {
	Decision         Decision `json:"decision"`
	Reason           string   `json:"reason,omitempty"`
	Method           string   `json:"method"`
	Path             string   `json:"path"`
	CredentialVia    []string `json:"credential_via"`
	UserAgent        string   `json:"user_agent,omitempty"`
	SessionID        string   `json:"session_id,omitempty"`
	Status           int      `json:"status,omitempty"`
	DurationMS       int64    `json:"duration_ms,omitempty"`
	ResponseBytes    int64    `json:"response_bytes,omitempty"`
	CredentialEchoed bool     `json:"credential_echoed,omitempty"`
}

type Recorder interface {
	Record(Event) error
}

type Config struct {
	Upstream    *url.URL
	Placeholder string
	Credential  Credential
	Recorder    Recorder
	Transport   http.RoundTripper
}

type route struct {
	method string
	path   string
}

var modelRoutes = map[route]bool{
	{http.MethodPost, "/v1/messages"}:              true,
	{http.MethodPost, "/v1/messages/count_tokens"}: true,
	{http.MethodGet, "/v1/models"}:                 true,
}

type Proxy struct {
	placeholder    string
	recorder       Recorder
	forward        *httputil.ReverseProxy
	evidenceBroken atomic.Bool
}

func New(cfg Config) (*Proxy, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	secret := cfg.Credential.secret
	return &Proxy{
		placeholder: cfg.Placeholder,
		recorder:    cfg.Recorder,
		forward: &httputil.ReverseProxy{
			FlushInterval: -1,
			Transport:     cfg.Transport,
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(cfg.Upstream)
				pr.Out.Header.Del("X-Api-Key")
				pr.Out.Header.Set("Authorization", "Bearer "+secret)
			},
			ModifyResponse: func(resp *http.Response) error {
				if o, ok := resp.Request.Context().Value(outcomeKey{}).(*outcome); ok {
					resp.Body = &echoScanner{ReadCloser: resp.Body, needle: []byte(secret), outcome: o}
				}
				return nil
			},
		},
	}, nil
}

func (cfg Config) validate() error {
	switch {
	case cfg.Upstream == nil || cfg.Upstream.Scheme != "https" || cfg.Upstream.Host == "":
		return errors.New("upstream must be an https URL")
	case cfg.Credential.Len() == 0:
		return errors.New("credential is required")
	case cfg.Placeholder == "" || strings.Contains(cfg.Credential.secret, cfg.Placeholder):
		return errors.New("placeholder must be set and unrelated to the credential")
	case cfg.Recorder == nil:
		return errors.New("recorder is required")
	}
	return nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	via, presented := presentedCredentials(r.Header)
	ev := Event{
		Method:        r.Method,
		Path:          r.URL.Path,
		CredentialVia: via,
		UserAgent:     r.UserAgent(),
		SessionID:     r.Header.Get(sessionHeader),
	}
	if p.evidenceBroken.Load() {
		refuse(w, http.StatusServiceUnavailable, reasonEvidence)
		return
	}
	if reason := p.refusal(r, presented); reason != "" {
		ev.Decision, ev.Reason = Refused, reason
		if !p.record(ev) {
			refuse(w, http.StatusServiceUnavailable, reasonEvidence)
			return
		}
		refuse(w, http.StatusForbidden, reason)
		return
	}
	ev.Decision = Forwarded
	if !p.record(ev) {
		refuse(w, http.StatusServiceUnavailable, reasonEvidence)
		return
	}
	start := time.Now()
	out := &outcome{}
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	p.forward.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), outcomeKey{}, out)))
	ev.Decision = Completed
	ev.Status = sw.status
	ev.DurationMS = time.Since(start).Milliseconds()
	ev.ResponseBytes = out.bytes
	ev.CredentialEchoed = out.echoed
	p.record(ev)
}

func (p *Proxy) record(ev Event) bool {
	if err := p.recorder.Record(ev); err != nil {
		p.evidenceBroken.Store(true)
		return false
	}
	return true
}

func (p *Proxy) refusal(r *http.Request, presented []string) string {
	if !modelRoutes[route{r.Method, r.URL.Path}] {
		return reasonRoute
	}
	if len(presented) == 0 {
		return reasonCredential
	}
	for _, v := range presented {
		if v != p.placeholder {
			return reasonCredential
		}
	}
	return ""
}

func presentedCredentials(h http.Header) ([]string, []string) {
	via := []string{}
	values := []string{}
	if v := h.Get("Authorization"); v != "" {
		via = append(via, "authorization")
		values = append(values, strings.TrimPrefix(v, "Bearer "))
	}
	if v := h.Get("X-Api-Key"); v != "" {
		via = append(via, "x-api-key")
		values = append(values, v)
	}
	return via, values
}

func refuse(w http.ResponseWriter, status int, reason string) {
	body, _ := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": "permission_error", "message": "model proxy: " + reason},
	})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(shouldRetryHeader, "false")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

type outcomeKey struct{}

type outcome struct {
	bytes  int64
	echoed bool
}

type echoScanner struct {
	io.ReadCloser
	needle  []byte
	tail    []byte
	outcome *outcome
}

func (s *echoScanner) Read(p []byte) (int, error) {
	n, err := s.ReadCloser.Read(p)
	if n > 0 {
		s.outcome.bytes += int64(n)
		window := append(s.tail, p[:n]...)
		if bytes.Contains(window, s.needle) {
			s.outcome.echoed = true
		}
		keep := min(len(window), len(s.needle)-1)
		s.tail = append(s.tail[:0:0], window[len(window)-keep:]...)
	}
	return n, err
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
