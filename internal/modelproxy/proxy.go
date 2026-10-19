package modelproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

type Decision string

const (
	Refused          Decision = "refused"
	Forwarded        Decision = "forwarded"
	Completed        Decision = "completed"
	RefreshRequested Decision = "credential_refresh_requested"
	RefreshRotated   Decision = "credential_refresh_rotated"
	RefreshFailed    Decision = "credential_refresh_failed"
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
	*modelProxy
}

type staticModelSource struct {
	credential  modelCredential
	invalidated atomic.Bool
}

func (s *staticModelSource) Resolve(context.Context) (modelCredential, error) {
	if s.invalidated.Load() {
		return modelCredential{}, errors.New("host model login is unavailable")
	}
	return s.credential, nil
}

func (s *staticModelSource) Invalidate(token string) {
	if token == s.credential.secret {
		s.invalidated.Store(true)
	}
}

func New(cfg Config) (*Proxy, error) {
	if err := validateModelProxy(cfg.Upstream, cfg.Placeholder, cfg.Recorder); err != nil {
		return nil, err
	}
	if cfg.Credential.Len() == 0 || strings.Contains(cfg.Credential.secret, cfg.Placeholder) {
		return nil, errors.New("host credential must be set and unrelated to the placeholder")
	}
	return &Proxy{modelProxy: &modelProxy{
		upstream: cfg.Upstream, placeholder: cfg.Placeholder, recorder: cfg.Recorder, transport: cfg.Transport,
		profile: modelProfile{routes: modelRoutes, allowAPIKey: true, allowDualAuth: true},
		source:  &staticModelSource{credential: modelCredential{secret: cfg.Credential.secret}},
	}}, nil
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

type outcome struct {
	bytes  int64
	echoed bool
}

type echoScanner struct {
	io.ReadCloser
	needle  []byte
	pending []byte
	ready   []byte
	eof     bool
	readErr error
	outcome *outcome
}

func (s *echoScanner) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(s.ready) == 0 {
		if s.readErr != nil {
			return 0, s.readErr
		}
		if s.eof {
			if len(s.pending) == 0 {
				return 0, io.EOF
			}
			s.ready = append(s.ready, s.pending...)
			s.pending = nil
			break
		}
		var chunk [4096]byte
		n, err := s.ReadCloser.Read(chunk[:])
		for _, b := range chunk[:n] {
			s.pending = append(s.pending, b)
			for len(s.pending) > 0 && !bytes.HasPrefix(s.needle, s.pending) {
				s.ready = append(s.ready, s.pending[0])
				s.pending = s.pending[1:]
			}
			if bytes.Equal(s.pending, s.needle) {
				s.ready = append(s.ready, "[redacted]"...)
				s.pending = nil
				s.outcome.echoed = true
			}
		}
		s.outcome.bytes += int64(n)
		if err == io.EOF {
			s.eof = true
		} else if err != nil {
			s.readErr = err
			s.pending = nil
			if len(s.ready) == 0 {
				return 0, err
			}
		} else if n == 0 {
			return 0, nil
		}
	}
	n := copy(p, s.ready)
	s.ready = s.ready[n:]
	return n, nil
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
