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
	"strconv"
	"strings"
	"sync"
	"time"
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
	usage nativeUsage
}

type nativeUsage struct {
	input      int64
	output     int64
	cacheRead  int64
	cacheWrite int64
	reasoning  int64
	total      int64
	costUSD    float64
	model      string
	recorded   bool
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
	walkMaps(payload, func(item map[string]any) {
		attributes := decodeOTLPAttributes(item["attributes"])
		eventName := stringAttribute(attributes, "event.name")
		if eventName == "" {
			eventName = otlpString(item["body"])
		}
		var usage nativeUsage
		switch eventName {
		case "claude_code.api_request":
			usage.input = intAttribute(attributes, "input_tokens")
			usage.output = intAttribute(attributes, "output_tokens")
			usage.cacheRead = intAttribute(attributes, "cache_read_tokens")
			usage.cacheWrite = intAttribute(attributes, "cache_creation_tokens")
			usage.total = usage.input + usage.output + usage.cacheRead + usage.cacheWrite
			usage.costUSD = floatAttribute(attributes, "cost_usd")
		case "codex.sse_event":
			if stringAttribute(attributes, "event.kind") != "response.completed" {
				return
			}
			usage.input = intAttribute(attributes, "input_token_count")
			usage.output = intAttribute(attributes, "output_token_count")
			usage.cacheRead = intAttribute(attributes, "cached_token_count")
			usage.reasoning = intAttribute(attributes, "reasoning_token_count")
			usage.total = usage.input + usage.output
		default:
			return
		}
		if usage.total <= 0 {
			return
		}
		usage.model = stringAttribute(attributes, "model")
		usage.recorded = true
		r.mu.Lock()
		r.usage.input += usage.input
		r.usage.output += usage.output
		r.usage.cacheRead += usage.cacheRead
		r.usage.cacheWrite += usage.cacheWrite
		r.usage.reasoning += usage.reasoning
		r.usage.total += usage.total
		r.usage.costUSD += usage.costUSD
		if usage.model != "" {
			r.usage.model = usage.model
		}
		r.usage.recorded = true
		r.mu.Unlock()
	})
}

func (r *NativeRelay) recordUsage() {
	r.mu.Lock()
	usage := r.usage
	r.mu.Unlock()
	if !usage.recorded {
		return
	}
	if usage.model != "" {
		r.recorder.ObserveModel(usage.model, "", "native_otel")
	}
	result := Usage{
		Status:           "observed",
		InputTokens:      int64Value(usage.input),
		OutputTokens:     int64Value(usage.output),
		CacheReadTokens:  int64Value(usage.cacheRead),
		CacheWriteTokens: int64Value(usage.cacheWrite),
		ReasoningTokens:  int64Value(usage.reasoning),
		TotalTokens:      int64Value(usage.total),
		Source:           "native_otel",
		Scope:            "run",
		Precision:        "request",
		Confidence:       "provider_reported",
	}
	if usage.costUSD > 0 {
		cost := strconv.FormatFloat(usage.costUSD, 'f', 6, 64)
		result.CostAmount = &cost
		result.CostCurrency = "USD"
	}
	r.recorder.RecordUsage(result)
}

func int64Value(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func walkMaps(value any, visit func(map[string]any)) {
	switch typed := value.(type) {
	case map[string]any:
		visit(typed)
		for _, child := range typed {
			walkMaps(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkMaps(child, visit)
		}
	}
}

func decodeOTLPAttributes(value any) map[string]any {
	result := make(map[string]any)
	items, _ := value.([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		key, _ := item["key"].(string)
		if key != "" {
			result[key] = otlpValue(item["value"])
		}
	}
	return result
}

func otlpValue(value any) any {
	item, _ := value.(map[string]any)
	for _, key := range []string{"stringValue", "intValue", "doubleValue", "boolValue"} {
		if found, ok := item[key]; ok {
			return found
		}
	}
	return nil
}

func otlpString(value any) string {
	if text, ok := otlpValue(value).(string); ok {
		return text
	}
	return ""
}

func stringAttribute(attributes map[string]any, key string) string {
	value, _ := attributes[key].(string)
	return value
}

func intAttribute(attributes map[string]any, key string) int64 {
	switch value := attributes[key].(type) {
	case string:
		result, _ := strconv.ParseInt(value, 10, 64)
		return result
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func floatAttribute(attributes map[string]any, key string) float64 {
	switch value := attributes[key].(type) {
	case string:
		result, _ := strconv.ParseFloat(value, 64)
		return result
	case float64:
		return value
	default:
		return 0
	}
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
