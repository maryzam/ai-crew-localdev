package phoenix

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultCollectorEndpoint = "http://127.0.0.1:4319"
	LiveProjectionTimeout    = 200 * time.Millisecond
	ReplayProjectionTimeout  = 10 * time.Second
)

type Projector struct {
	endpoint string
	client   *http.Client
}

func NewProjector(endpoint string, requestTimeout time.Duration) (*Projector, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse Phoenix Collector endpoint: %w", err)
	}
	if parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return nil, errors.New("phoenix collector endpoint must be an HTTP origin")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("phoenix collector endpoint must be on loopback")
	}
	if requestTimeout <= 0 {
		return nil, errors.New("phoenix collector request timeout must be positive")
	}
	return &Projector{endpoint: strings.TrimSuffix(endpoint, "/"), client: &http.Client{Timeout: requestTimeout}}, nil
}

func (p *Projector) Project(ctx context.Context, payload []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/v1/traces", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("phoenix collector returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	return nil
}
