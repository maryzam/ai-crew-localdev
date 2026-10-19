package runtelemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

const (
	MaxRequestBytes     = 32 << 20
	ReceiverStatsEvent  = "telemetry.receiver"
	routePrefix         = "/otlp/"
	otlpSuccessResponse = "{}"
)

var signalGroups = map[string]string{"logs": "resourceLogs", "metrics": "resourceMetrics", "traces": "resourceSpans"}

type Stats struct {
	Stored   uint64 `json:"stored"`
	Rejected uint64 `json:"rejected"`
	Failed   uint64 `json:"failed"`
}

type statsSnapshot struct {
	Instance string `json:"instance"`
	Stats
}

type Receiver struct {
	context  Context
	root     *evidence.Root
	owned    map[string]bool
	instance string
	stored   atomic.Uint64
	rejected atomic.Uint64
	failed   atomic.Uint64
}

func NewReceiver(c Context, root *evidence.Root) (*Receiver, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errors.New("telemetry receiver needs an evidence root")
	}
	owned := map[string]bool{}
	for _, key := range contextKeys() {
		owned[key] = true
	}
	var instance [16]byte
	if _, err := rand.Read(instance[:]); err != nil {
		return nil, fmt.Errorf("create receiver instance ID: %w", err)
	}
	return &Receiver{context: c, root: root, owned: owned, instance: hex.EncodeToString(instance[:])}, nil
}

func (r *Receiver) Stats() Stats {
	return Stats{Stored: r.stored.Load(), Rejected: r.rejected.Load(), Failed: r.failed.Load()}
}

func (r *Receiver) RecordStats(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var recorded Stats
	for {
		select {
		case <-ticker.C:
			if s := r.Stats(); s != recorded && r.recordStats(s) == nil {
				recorded = s
			}
		case <-ctx.Done():
			return r.recordStats(r.Stats())
		}
	}
}

func (r *Receiver) recordStats(s Stats) error {
	return r.root.AppendRunEvent(r.context.RunID, ReceiverStatsEvent, statsSnapshot{Instance: r.instance, Stats: s})
}

func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	agent, group, ok := route(req.URL.Path)
	if _, known := r.context.Agents[agent]; !ok || !known {
		r.reject(w, http.StatusNotFound, "unknown telemetry route")
		return
	}
	if req.Method != http.MethodPost {
		r.reject(w, http.StatusMethodNotAllowed, "telemetry accepts POST only")
		return
	}
	if media, _, err := mime.ParseMediaType(req.Header.Get("Content-Type")); err != nil || media != "application/json" {
		r.reject(w, http.StatusUnsupportedMediaType, "telemetry accepts OTLP/HTTP JSON only")
		return
	}
	body, status, err := readBody(w, req)
	if err != nil {
		r.reject(w, status, err.Error())
		return
	}
	line, err := r.stamp(body, group, r.context.attributes(agent))
	if err != nil {
		r.reject(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := r.root.AppendTelemetry(r.context.RunID, line); err != nil {
		r.failed.Add(1)
		http.Error(w, "telemetry was not stored", http.StatusInternalServerError)
		return
	}
	r.stored.Add(1)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, otlpSuccessResponse)
}

func route(path string) (Agent, string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, routePrefix), "/")
	if !strings.HasPrefix(path, routePrefix) || len(parts) != 3 || parts[1] != "v1" || signalGroups[parts[2]] == "" {
		return "", "", false
	}
	return Agent(parts[0]), signalGroups[parts[2]], true
}

func readBody(w http.ResponseWriter, req *http.Request) ([]byte, int, error) {
	var reader io.Reader = http.MaxBytesReader(w, req.Body, MaxRequestBytes)
	switch req.Header.Get("Content-Encoding") {
	case "", "identity":
	case "gzip":
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, http.StatusBadRequest, fmt.Errorf("invalid gzip body: %w", err)
		}
		defer func() { _ = gz.Close() }()
		reader = io.LimitReader(gz, MaxRequestBytes+1)
	default:
		return nil, http.StatusUnsupportedMediaType, errors.New("unsupported content encoding")
	}
	body, err := io.ReadAll(reader)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) || len(body) > MaxRequestBytes {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("telemetry request exceeds %d bytes", MaxRequestBytes)
	}
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("unreadable telemetry body: %w", err)
	}
	return body, 0, nil
}

func (r *Receiver) stamp(body []byte, group string, attrs []attribute) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("invalid OTLP JSON: %w", err)
	}
	if decoder.More() {
		return nil, errors.New("invalid OTLP JSON: trailing data")
	}
	for key := range payload {
		if strings.HasPrefix(key, "resource") && key != group {
			return nil, fmt.Errorf("%s does not belong on this signal route", key)
		}
	}
	groups, ok := payload[group].([]any)
	if !ok {
		return nil, fmt.Errorf("OTLP JSON has no %s array", group)
	}
	for _, g := range groups {
		entry, ok := g.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s entries must be objects", group)
		}
		resource, _ := entry["resource"].(map[string]any)
		if resource == nil {
			resource = map[string]any{}
			entry["resource"] = resource
		}
		existing, _ := resource["attributes"].([]any)
		kept := make([]any, 0, len(existing)+len(attrs))
		for _, a := range existing {
			if kv, ok := a.(map[string]any); ok {
				if key, _ := kv["key"].(string); r.owned[key] {
					continue
				}
			}
			kept = append(kept, a)
		}
		for _, a := range attrs {
			if a.value != "" {
				kept = append(kept, map[string]any{"key": a.key, "value": map[string]any{"stringValue": a.value}})
			}
		}
		resource["attributes"] = kept
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

func (r *Receiver) reject(w http.ResponseWriter, status int, message string) {
	r.rejected.Add(1)
	http.Error(w, message, status)
}
