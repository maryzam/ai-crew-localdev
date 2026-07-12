package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/runevents"
)

const (
	nativeRequestLimit = 512 * 1024
	nativeQueueSize    = 16
)

type NativeRelay struct {
	recorder  *Recorder
	exporter  OTLPExporter
	server    *http.Server
	listener  net.Listener
	token     string
	queue     chan []byte
	worker    sync.WaitGroup
	handlers  sync.RWMutex
	closing   bool
	closeOnce sync.Once
	warnOnce  sync.Once

	mu    sync.Mutex
	usage runevents.NativeUsageAccumulator
}

func StartNativeRelay(recorder *Recorder, exporter OTLPExporter) (*NativeRelay, error) {
	if recorder == nil || recorder.disabled {
		return nil, fmt.Errorf("start native telemetry relay: telemetry is disabled")
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate native telemetry token: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start native telemetry relay: %w", err)
	}
	if exporter != nil {
		if err := recorder.ConfigureOTLP(exporter); err != nil {
			_ = listener.Close()
			return nil, err
		}
	}
	relay := &NativeRelay{
		recorder: recorder,
		exporter: exporter,
		listener: listener,
		token:    hex.EncodeToString(tokenBytes),
		queue:    make(chan []byte, nativeQueueSize),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", relay.handleLogs)
	mux.HandleFunc("/v1/traces", relay.handleTraces)
	relay.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	if exporter != nil {
		relay.worker.Add(1)
		go relay.exportWorker()
	}
	go func() {
		if err := relay.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			relay.warn(fmt.Errorf("native telemetry relay: %w", err))
		}
	}()
	return relay, nil
}

func (r *NativeRelay) Endpoint() string {
	return "http://" + r.listener.Addr().String()
}

func (r *NativeRelay) Authorization() string {
	return "Bearer " + r.token
}

func (r *NativeRelay) Close() {
	r.closeOnce.Do(func() {
		r.handlers.Lock()
		r.closing = true
		r.handlers.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := r.server.Shutdown(ctx); err != nil {
			_ = r.server.Close()
		}
		cancel()
		close(r.queue)
		r.worker.Wait()
		r.recordUsage()
	})
}

func (r *NativeRelay) handleLogs(w http.ResponseWriter, request *http.Request) {
	r.handleSignal(w, request, false)
}

func (r *NativeRelay) handleTraces(w http.ResponseWriter, request *http.Request) {
	r.handleSignal(w, request, true)
}

func (r *NativeRelay) handleSignal(w http.ResponseWriter, request *http.Request, trace bool) {
	r.handlers.RLock()
	defer r.handlers.RUnlock()
	if r.closing {
		http.Error(w, "relay closing", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost || !r.authorized(request.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, nativeRequestLimit+1))
	if err != nil || len(body) > nativeRequestLimit {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	if !trace {
		var payload any
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid OTLP JSON", http.StatusBadRequest)
			return
		}
		r.collectUsage(payload)
	} else {
		var payload otlpPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid OTLP JSON", http.StatusBadRequest)
			return
		}
		if r.exporter == nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
			return
		}
		payloads, dropped := sanitizeNativePayloads(payload, r.recorder.Summary())
		if dropped > 0 {
			r.warn(fmt.Errorf("native telemetry relay dropped %d malformed spans", dropped))
		}
		if len(payloads) == 0 && dropped > 0 {
			http.Error(w, "invalid trace", http.StatusBadRequest)
			return
		}
		for _, sanitized := range payloads {
			encoded, err := json.Marshal(sanitized)
			if err != nil {
				http.Error(w, "invalid trace", http.StatusBadRequest)
				return
			}
			select {
			case r.queue <- encoded:
			default:
				r.warn(fmt.Errorf("native telemetry export queue full"))
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{}`)
}

func (r *NativeRelay) authorized(value string) bool {
	want := []byte("Bearer " + r.token)
	got := []byte(value)
	return len(got) == len(want) && subtle.ConstantTimeCompare(got, want) == 1
}

func (r *NativeRelay) exportWorker() {
	defer r.worker.Done()
	for payload := range r.queue {
		if err := r.exporter.Export(payload); err != nil {
			r.warn(err)
		}
	}
}

func (r *NativeRelay) warn(err error) {
	r.warnOnce.Do(func() {
		_, _ = fmt.Fprintf(otlpWarnings, "warning: native OTLP telemetry failed: %v\n", err)
	})
}

func (r *NativeRelay) collectUsage(payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage.CollectOTLPLogs(payload)
}

func (r *NativeRelay) recordUsage() {
	r.mu.Lock()
	usage := r.usage.Snapshot()
	r.mu.Unlock()
	if !usage.Recorded {
		return
	}
	if usage.Model != "" {
		r.recorder.ObserveModel(usage.Model, "", "native_otel")
	}
	r.recorder.RecordUsage(usage.RunUsage())
}

var nativeAttributeAliases = map[string]FieldID{
	"model":                 "gen_ai.request.model",
	"input_tokens":          "gen_ai.usage.input_tokens",
	"output_tokens":         "gen_ai.usage.output_tokens",
	"cache_read_tokens":     "gen_ai.usage.cache_read.input_tokens",
	"cache_creation_tokens": "ai_agent.usage.cache_write.input_tokens",
	"input_token_count":     "gen_ai.usage.input_tokens",
	"output_token_count":    "gen_ai.usage.output_tokens",
	"cached_token_count":    "gen_ai.usage.cache_read.input_tokens",
	"reasoning_token_count": "gen_ai.usage.reasoning.output_tokens",
}

func sanitizeNativePayloads(payload otlpPayload, summary RunSummary) ([]otlpPayload, int) {
	var payloads []otlpPayload
	dropped := 0
	for resourceIndex := range payload.ResourceSpans {
		resource := &payload.ResourceSpans[resourceIndex]
		attributes := sanitizeNativeAttributes(resource.Resource.Attributes, summary, true, MaxRootAttributes)
		spans := make([]otlpSpan, 0)
		for scopeIndex := range resource.ScopeSpans {
			for spanIndex := range resource.ScopeSpans[scopeIndex].Spans {
				span := resource.ScopeSpans[scopeIndex].Spans[spanIndex]
				if sanitizeNativeSpan(&span, summary) {
					spans = append(spans, span)
				} else {
					dropped++
				}
			}
		}
		for start := 0; start < len(spans); start += MaxExportSpans {
			end := min(start+MaxExportSpans, len(spans))
			payloads = append(payloads, otlpPayload{ResourceSpans: []otlpResourceSpans{{
				Resource: otlpResource{Attributes: attributes},
				ScopeSpans: []otlpScopeSpans{{
					Scope: otlpScope{Name: nativeScopeName},
					Spans: spans[start:end],
				}},
			}}})
		}
	}
	return payloads, dropped
}

func sanitizeNativeSpan(span *otlpSpan, summary RunSummary) bool {
	span.Name = safeNativeName(span.Name, nativeSpanFallback)
	span.TraceID = safeHex(span.TraceID, 32)
	span.SpanID = safeHex(span.SpanID, 16)
	span.ParentSpanID = safeHex(span.ParentSpanID, 16)
	if span.Kind < 0 || span.Kind > 5 {
		span.Kind = 0
	}
	span.StartTimeUnixNano = safeDecimal(span.StartTimeUnixNano)
	span.EndTimeUnixNano = safeDecimal(span.EndTimeUnixNano)
	if span.Status.Code < 0 || span.Status.Code > 2 {
		span.Status.Code = 0
	}
	span.Status.Message = ""
	span.Attributes = sanitizeNativeAttributes(span.Attributes, summary, true, MaxRootAttributes)
	if span.TraceID == "" || span.SpanID == "" || span.StartTimeUnixNano == "" || span.EndTimeUnixNano == "" {
		return false
	}
	events := make([]otlpSpanEvent, 0, min(len(span.Events), MaxExportSpanEvents))
	for eventIndex := range span.Events {
		event := span.Events[eventIndex]
		event.Name = safeNativeName(event.Name, nativeEventFallback)
		event.TimeUnixNano = safeDecimal(event.TimeUnixNano)
		if event.TimeUnixNano == "" {
			continue
		}
		event.Attributes = sanitizeNativeAttributes(event.Attributes, RunSummary{}, false, MaxEventAttributes)
		events = append(events, event)
		if len(events) == MaxExportSpanEvents {
			break
		}
	}
	span.Events = events
	return true
}

func sanitizeNativeAttributes(attributes []otlpWireAttribute, summary RunSummary, correlate bool, limit int) []otlpWireAttribute {
	inputLimit := limit
	if correlate {
		inputLimit = max(0, inputLimit-nativeCorrelationCount(summary))
	}
	result := make([]otlpWireAttribute, 0, limit)
	seen := make(map[string]struct{})
	for _, attribute := range attributes {
		if len(result) >= inputLimit {
			break
		}
		key, policy, ok := nativeField(attribute.Key)
		if !ok {
			continue
		}
		if _, exists := seen[string(key)]; exists {
			continue
		}
		value, ok := sanitizeOTLPValue(attribute.Value, policy.MaxLength)
		if !ok {
			continue
		}
		result = append(result, otlpWireAttribute{Key: string(key), Value: value})
		seen[string(key)] = struct{}{}
	}
	if !correlate {
		return result
	}
	result = appendNativeCorrelation(result, seen, "ai_agent.run.id", "ai_agent.run.id", summary.RunID)
	result = appendNativeCorrelation(result, seen, "ai_agent.repository.slug", "ai_agent.repository.slug", summary.Repository.Slug)
	result = appendNativeCorrelation(result, seen, "ai_agent.agent.type", "ai_agent.agent.type", summary.Agent.Type)
	result = appendNativeCorrelation(result, seen, "ai_agent.task.ref", "ai_agent.task.ref", summary.Task.Ref)
	result = appendNativeCorrelation(result, seen, "ai_agent.task.ref", "langfuse.session.id", summary.Task.Ref)
	return result
}

func nativeCorrelationCount(summary RunSummary) int {
	count := 0
	for _, value := range []string{summary.RunID, summary.Repository.Slug, summary.Agent.Type, summary.Task.Ref} {
		if value != "" {
			count++
		}
	}
	if summary.Task.Ref != "" {
		count++
	}
	return count
}

func nativeField(key string) (FieldID, FieldPolicy, bool) {
	field := FieldID(key)
	if alias, ok := nativeAttributeAliases[key]; ok {
		field = alias
	}
	policy, ok := fieldPolicy(string(field))
	return field, policy, ok && policy.NativeInput && fieldAllowed(field, "otlp") && !policy.Sensitive
}

func appendNativeCorrelation(attributes []otlpWireAttribute, seen map[string]struct{}, source FieldID, key, value string) []otlpWireAttribute {
	policy, ok := fieldPolicy(string(source))
	if !ok || policy.Sensitive || !fieldAllowed(source, "otlp") || value == "" {
		return attributes
	}
	if _, exists := seen[key]; exists {
		return attributes
	}
	return append(attributes, newOTLPWireAttribute(key, bounded(value, policy.MaxLength)))
}

func sanitizeOTLPValue(value otlpWireValue, maxLength int) (otlpWireValue, bool) {
	if value.StringValue != nil {
		if maxLength <= 0 {
			maxLength = MaxPropagatedValueLength
		}
		text := bounded(*value.StringValue, maxLength)
		return otlpWireValue{StringValue: &text}, true
	}
	if value.IntValue != nil && decimal(*value.IntValue) {
		return otlpWireValue{IntValue: value.IntValue}, true
	}
	if value.DoubleValue != nil {
		return otlpWireValue{DoubleValue: value.DoubleValue}, true
	}
	if value.BoolValue != nil {
		return otlpWireValue{BoolValue: value.BoolValue}, true
	}
	return otlpWireValue{}, false
}

func safeNativeName(name, fallback string) string {
	if exportNameHasAllowedPrefix(name) {
		return bounded(name, MaxExportNameLength)
	}
	return fallback
}

func safeHex(value string, length int) string {
	if len(value) != length {
		return ""
	}
	if _, err := hex.DecodeString(value); err == nil {
		return strings.ToLower(value)
	}
	return ""
}

func safeDecimal(value string) string {
	if value != "" && len(value) <= 24 && decimal(value) {
		return value
	}
	return ""
}

func decimal(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if char == '-' && index == 0 {
			continue
		}
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
