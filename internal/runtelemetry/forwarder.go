package runtelemetry

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

const (
	MaxRequestBytes  = 32 << 20
	maxResponseBytes = 1 << 20
	upstreamTimeout  = 10 * time.Second
	routePrefix      = "/otlp/"
)

var signals = map[string]bool{"logs": true, "metrics": true, "traces": true}

var forwardedHeaders = []string{"Content-Type", "Content-Encoding"}

const ForwarderStatsEvent = "telemetry.forwarder"

type Stats struct {
	Forwarded uint64 `json:"forwarded"`
	Rejected  uint64 `json:"rejected"`
	Failed    uint64 `json:"failed"`
}

type Forwarder struct {
	context   Context
	client    *http.Client
	forwarded atomic.Uint64
	rejected  atomic.Uint64
	failed    atomic.Uint64
}

func NewForwarder(c Context, collectorSocket string) (*Forwarder, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var dialer net.Dialer
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", collectorSocket)
		},
	}
	return &Forwarder{context: c, client: &http.Client{Transport: transport, Timeout: upstreamTimeout}}, nil
}

func (f *Forwarder) Stats() Stats {
	return Stats{Forwarded: f.forwarded.Load(), Rejected: f.rejected.Load(), Failed: f.failed.Load()}
}

func (f *Forwarder) RecordStats(ctx context.Context, root *evidence.Root, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var recorded Stats
	for {
		select {
		case <-ticker.C:
			if s := f.Stats(); s != recorded {
				if err := root.AppendRunEvent(f.context.RunID, ForwarderStatsEvent, s); err != nil {
					return err
				}
				recorded = s
			}
		case <-ctx.Done():
			return root.AppendRunEvent(f.context.RunID, ForwarderStatsEvent, f.Stats())
		}
	}
}

func (f *Forwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	agent, signal, ok := route(r.URL.Path)
	if _, known := f.context.Agents[agent]; !ok || !known {
		f.reject(w, http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		f.reject(w, http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		f.reject(w, http.StatusRequestEntityTooLarge)
		return
	}
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://collector/v1/"+signal, bytes.NewReader(body))
	if err != nil {
		f.fail(w, http.StatusInternalServerError)
		return
	}
	for _, name := range forwardedHeaders {
		if v := r.Header.Get(name); v != "" {
			upstream.Header.Set(name, v)
		}
	}
	for _, a := range f.context.attributes(agent) {
		if a.value != "" {
			upstream.Header.Set(headerName(a.key), a.value)
		}
	}
	resp, err := f.client.Do(upstream)
	if err != nil {
		f.fail(w, http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusMultipleChoices {
		f.failed.Add(1)
	} else {
		f.forwarded.Add(1)
	}
	if v := resp.Header.Get("Content-Type"); v != "" {
		w.Header().Set("Content-Type", v)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxResponseBytes))
}

func route(path string) (Agent, string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, routePrefix), "/")
	if !strings.HasPrefix(path, routePrefix) || len(parts) != 3 || parts[1] != "v1" || !signals[parts[2]] {
		return "", "", false
	}
	return Agent(parts[0]), parts[2], true
}

func (f *Forwarder) reject(w http.ResponseWriter, status int) {
	f.rejected.Add(1)
	http.Error(w, http.StatusText(status), status)
}

func (f *Forwarder) fail(w http.ResponseWriter, status int) {
	f.failed.Add(1)
	http.Error(w, http.StatusText(status), status)
}
