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
	"sync"
	"sync/atomic"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

const (
	MaxRequestBytes      = 32 << 20
	ReceiverStatsEvent   = "telemetry.receiver"
	routePrefix          = "/otlp/"
	otlpSuccessResponse  = "{}"
	projectionQueueSize  = 64
	projectionQueueBytes = 64 << 20
)

var signalGroups = map[string]string{"logs": "resourceLogs", "metrics": "resourceMetrics", "traces": "resourceSpans"}

type Stats struct {
	Stored           uint64 `json:"stored"`
	Rejected         uint64 `json:"rejected"`
	Failed           uint64 `json:"failed"`
	Projected        uint64 `json:"projected,omitempty"`
	ProjectionFailed uint64 `json:"projection_failed,omitempty"`
}

type View interface {
	Project(context.Context, []byte) error
}

type ReceiverOption func(*Receiver)

func WithView(view View) ReceiverOption {
	return func(receiver *Receiver) { receiver.view = view }
}

type statsSnapshot struct {
	Instance string `json:"instance"`
	Stats
}

type Receiver struct {
	context          Context
	root             *evidence.Root
	owned            map[string]bool
	instance         string
	stored           atomic.Uint64
	rejected         atomic.Uint64
	failed           atomic.Uint64
	projected        atomic.Uint64
	projectionFailed atomic.Uint64
	view             View
	projectionQueue  chan []byte
	projectionCancel context.CancelFunc
	projectionDone   chan struct{}
	projectionMu     sync.RWMutex
	projectionClosed bool
	projectionBytes  atomic.Int64
	closeOnce        sync.Once
}

func NewReceiver(c Context, root *evidence.Root, options ...ReceiverOption) (*Receiver, error) {
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
	receiver := &Receiver{context: c, root: root, owned: owned, instance: hex.EncodeToString(instance[:])}
	for _, option := range options {
		option(receiver)
	}
	if receiver.view != nil {
		projectionContext, cancel := context.WithCancel(context.Background())
		receiver.projectionQueue = make(chan []byte, projectionQueueSize)
		receiver.projectionCancel = cancel
		receiver.projectionDone = make(chan struct{})
		go receiver.project(projectionContext)
	}
	return receiver, nil
}

func (r *Receiver) Stats() Stats {
	return Stats{Stored: r.stored.Load(), Rejected: r.rejected.Load(), Failed: r.failed.Load(), Projected: r.projected.Load(), ProjectionFailed: r.projectionFailed.Load()}
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

func (r *Receiver) Close() error {
	r.closeOnce.Do(func() {
		if r.projectionCancel != nil {
			r.projectionMu.Lock()
			r.projectionClosed = true
			r.projectionCancel()
			r.projectionMu.Unlock()
			<-r.projectionDone
		}
	})
	return r.recordStats(r.Stats())
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
	status, err = r.store(agent, group, body)
	if err != nil {
		if status == http.StatusInternalServerError {
			http.Error(w, "telemetry was not stored", status)
		} else {
			r.reject(w, status, err.Error())
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, otlpSuccessResponse)
}

func (r *Receiver) Store(agent Agent, signal string, body []byte) error {
	group := signalGroups[signal]
	if group == "" {
		r.rejected.Add(1)
		return fmt.Errorf("unknown telemetry signal %q", signal)
	}
	status, err := r.store(agent, group, body)
	if err != nil && status != http.StatusInternalServerError {
		r.rejected.Add(1)
	}
	return err
}

func (r *Receiver) store(agent Agent, group string, body []byte) (int, error) {
	if _, known := r.context.Agents[agent]; !known {
		r.rejected.Add(1)
		return http.StatusNotFound, fmt.Errorf("unknown telemetry agent %q", agent)
	}
	line, err := r.stamp(body, group, r.context.attributes(agent))
	if err != nil {
		return http.StatusBadRequest, err
	}
	if err := r.root.AppendTelemetry(r.context.RunID, line); err != nil {
		r.failed.Add(1)
		return http.StatusInternalServerError, err
	}
	r.stored.Add(1)
	if group == signalGroups["traces"] {
		r.enqueueProjection(line)
	}
	return http.StatusOK, nil

}

func (r *Receiver) enqueueProjection(payload []byte) {
	if r.view == nil {
		return
	}
	r.projectionMu.RLock()
	defer r.projectionMu.RUnlock()
	if r.projectionClosed {
		r.projectionFailed.Add(1)
		return
	}
	if !r.reserveProjectionBytes(int64(len(payload))) {
		r.projectionFailed.Add(1)
		return
	}
	select {
	case r.projectionQueue <- append([]byte(nil), payload...):
	default:
		r.projectionBytes.Add(-int64(len(payload)))
		r.projectionFailed.Add(1)
	}
}

func (r *Receiver) reserveProjectionBytes(size int64) bool {
	for {
		current := r.projectionBytes.Load()
		if size > projectionQueueBytes-current {
			return false
		}
		if r.projectionBytes.CompareAndSwap(current, current+size) {
			return true
		}
	}
}

func (r *Receiver) project(ctx context.Context) {
	defer close(r.projectionDone)
	for {
		select {
		case payload := <-r.projectionQueue:
			r.projectionBytes.Add(-int64(len(payload)))
			if err := r.view.Project(ctx, payload); err != nil {
				r.projectionFailed.Add(1)
			} else {
				r.projected.Add(1)
			}
		case <-ctx.Done():
			for {
				select {
				case payload := <-r.projectionQueue:
					r.projectionBytes.Add(-int64(len(payload)))
					r.projectionFailed.Add(1)
				default:
					return
				}
			}
		}
	}
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
