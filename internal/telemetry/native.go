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
	config    OTLPConfig
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

func StartNativeRelay(recorder *Recorder, config OTLPConfig) (*NativeRelay, error) {
	if recorder == nil {
		return nil, fmt.Errorf("start native telemetry relay: recorder is required")
	}
	if !recorder.Enabled() {
		return nil, nil
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate native telemetry token: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start native telemetry relay: %w", err)
	}
	if err := recorder.ConfigureOTLP(config); err != nil {
		_ = listener.Close()
		return nil, err
	}
	relay := &NativeRelay{
		recorder: recorder,
		config:   config,
		listener: listener,
		token:    hex.EncodeToString(tokenBytes),
		queue:    make(chan []byte, nativeQueueSize),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", relay.handleLogs)
	mux.HandleFunc("/v1/metrics", relay.handleMetrics)
	mux.HandleFunc("/v1/traces", relay.handleTraces)
	relay.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	relay.worker.Add(1)
	go relay.exportWorker()
	go func() {
		if err := relay.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			relay.warn(fmt.Errorf("native telemetry relay: %w", err))
		}
	}()
	return relay, nil
}

func (r *NativeRelay) Endpoint() string {
	if r == nil {
		return ""
	}
	return "http://" + r.listener.Addr().String()
}

func (r *NativeRelay) Authorization() string {
	if r == nil {
		return ""
	}
	return "Bearer " + r.token
}

func (r *NativeRelay) Close() {
	if r == nil {
		return
	}
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
	r.handleSignal(w, request, "logs")
}

func (r *NativeRelay) handleMetrics(w http.ResponseWriter, request *http.Request) {
	r.handleSignal(w, request, "metrics")
}

func (r *NativeRelay) handleTraces(w http.ResponseWriter, request *http.Request) {
	r.handleSignal(w, request, "traces")
}

func (r *NativeRelay) handleSignal(w http.ResponseWriter, request *http.Request, signal string) {
	r.handlers.RLock()
	defer r.handlers.RUnlock()
	if r.closing {
		http.Error(w, "relay closing", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost || !r.authorized(request.Header.Get("Authorization")) {
		r.recorder.metrics.rejected(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, nativeRequestLimit+1))
	if err != nil || len(body) > nativeRequestLimit {
		r.recorder.metrics.rejected(1)
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		r.recorder.metrics.rejected(1)
		http.Error(w, "invalid OTLP JSON", http.StatusBadRequest)
		return
	}
	if signal == "logs" {
		r.collectUsage(payload)
	}
	if signal == "traces" {
		sanitized := sanitizeNativePayload(payload, r.recorder.Summary())
		encoded, err := json.Marshal(sanitized)
		if err != nil {
			r.recorder.metrics.rejected(1)
			http.Error(w, "invalid trace", http.StatusBadRequest)
			return
		}
		r.recorder.metrics.payload(len(encoded))
		select {
		case r.queue <- encoded:
			r.recorder.metrics.queue(len(r.queue))
		default:
			r.recorder.metrics.saturation(1)
			r.warn(fmt.Errorf("native telemetry export queue full"))
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
		r.recorder.metrics.queue(len(r.queue))
		started := r.recorder.metrics.started()
		if err := postOTLPJSON(r.config, payload); err != nil {
			r.recorder.metrics.dropped(1)
			r.warn(err)
		}
		r.recorder.metrics.exported(started)
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
			eventName, _ = item["eventName"].(string)
		}
		if eventName == "" {
			eventName = otlpString(item["body"])
		}
		var usage nativeUsage
		switch eventName {
		case "claude_code.api_request", "api_request":
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
	case json.Number:
		result, _ := value.Int64()
		return result
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

func sanitizeNativePayload(payload any, summary RunSummary) any {
	root, _ := payload.(map[string]any)
	return map[string]any{"resourceSpans": sanitizeResourceSpans(root["resourceSpans"], summary)}
}

func sanitizeResourceSpans(value any, summary RunSummary) []any {
	result := make([]any, 0)
	for _, raw := range anySlice(value) {
		item, _ := raw.(map[string]any)
		resource, _ := item["resource"].(map[string]any)
		result = append(result, map[string]any{
			"resource": map[string]any{
				"attributes": sanitizeNativeAttributes(anySlice(resource["attributes"]), summary, true),
			},
			"scopeSpans": sanitizeScopeSpans(item["scopeSpans"], summary),
		})
	}
	return result
}

func sanitizeScopeSpans(value any, summary RunSummary) []any {
	result := make([]any, 0)
	for _, raw := range anySlice(value) {
		item, _ := raw.(map[string]any)
		result = append(result, map[string]any{
			"scope": map[string]any{"name": "ai-agent-native"},
			"spans": sanitizeNativeSpans(item["spans"], summary),
		})
	}
	return result
}

func sanitizeNativeSpans(value any, summary RunSummary) []any {
	result := make([]any, 0)
	for _, raw := range anySlice(value) {
		item, _ := raw.(map[string]any)
		span := map[string]any{
			"name":       safeNativeName(item["name"], "agent.operation"),
			"attributes": sanitizeNativeAttributes(anySlice(item["attributes"]), summary, true),
			"events":     sanitizeNativeEvents(item["events"]),
		}
		copyHexField(span, item, "traceId", 32)
		copyHexField(span, item, "spanId", 16)
		copyHexField(span, item, "parentSpanId", 16)
		copyIntegerField(span, item, "kind", 0, 5)
		copyDecimalField(span, item, "startTimeUnixNano")
		copyDecimalField(span, item, "endTimeUnixNano")
		if status, ok := item["status"].(map[string]any); ok {
			clean := make(map[string]any)
			copyIntegerField(clean, status, "code", 0, 2)
			span["status"] = clean
		}
		result = append(result, span)
	}
	return result
}

func sanitizeNativeEvents(value any) []any {
	result := make([]any, 0)
	for _, raw := range anySlice(value) {
		item, _ := raw.(map[string]any)
		event := map[string]any{
			"name":       safeNativeName(item["name"], "agent.event"),
			"attributes": sanitizeNativeAttributes(anySlice(item["attributes"]), RunSummary{}, false),
		}
		copyDecimalField(event, item, "timeUnixNano")
		result = append(result, event)
	}
	return result
}

func sanitizeNativeAttributes(attributes []any, summary RunSummary, correlate bool) []any {
	result := make([]any, 0, len(attributes)+8)
	seen := make(map[string]struct{})
	for _, raw := range attributes {
		item, _ := raw.(map[string]any)
		rawKey, _ := item["key"].(string)
		key, policy, ok := nativeField(rawKey)
		if !ok {
			continue
		}
		value := sanitizeOTLPValue(item["value"], policy.MaxLength)
		if value == nil {
			continue
		}
		result = append(result, map[string]any{"key": string(key), "value": value})
		seen[string(key)] = struct{}{}
	}
	if !correlate {
		return result
	}
	result = appendNativeCorrelation(result, seen, "ai_agent.run.id", "ai_agent.run.id", summary.RunID)
	result = appendNativeCorrelation(result, seen, "ai_agent.repository.slug", "ai_agent.repository.slug", summary.Repository.Slug)
	result = appendNativeCorrelation(result, seen, "ai_agent.agent.type", "ai_agent.agent.type", summary.Agent.Type)
	result = appendNativeCorrelation(result, seen, "ai_agent.run.id", "langfuse.trace.metadata.run_id", summary.RunID)
	result = appendNativeCorrelation(result, seen, "ai_agent.repository.slug", "langfuse.trace.metadata.repo", summary.Repository.Slug)
	result = appendNativeCorrelation(result, seen, "ai_agent.agent.type", "langfuse.trace.metadata.agent", summary.Agent.Type)
	result = appendNativeCorrelation(result, seen, "ai_agent.task.ref", "langfuse.trace.metadata.task_ref", summary.Task.Ref)
	result = appendNativeCorrelation(result, seen, "ai_agent.task.ref", "langfuse.session.id", summary.Task.Ref)
	return result
}

func nativeField(key string) (FieldID, FieldPolicy, bool) {
	field := FieldID(key)
	if alias, ok := nativeAttributeAliases[key]; ok {
		field = alias
	}
	policy, ok := fieldPolicy(string(field))
	return field, policy, ok && policy.NativeInput && fieldAllowed(field, "otlp") && !policy.Sensitive
}

func appendNativeCorrelation(attributes []any, seen map[string]struct{}, source FieldID, key, value string) []any {
	policy, ok := fieldPolicy(string(source))
	if !ok || policy.Sensitive || !fieldAllowed(source, "otlp") || value == "" {
		return attributes
	}
	if _, exists := seen[key]; exists {
		return attributes
	}
	return append(attributes, otlpAttribute(key, bounded(value, policy.MaxLength)))
}

func sanitizeOTLPValue(value any, maxLength int) map[string]any {
	item, _ := value.(map[string]any)
	if text, ok := item["stringValue"].(string); ok {
		return map[string]any{"stringValue": bounded(text, maxLength)}
	}
	if number, ok := item["intValue"].(string); ok && decimal(number) {
		return map[string]any{"intValue": number}
	}
	if number, ok := item["doubleValue"].(float64); ok {
		return map[string]any{"doubleValue": number}
	}
	if boolean, ok := item["boolValue"].(bool); ok {
		return map[string]any{"boolValue": boolean}
	}
	return nil
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func safeNativeName(value any, fallback string) string {
	name, _ := value.(string)
	if strings.HasPrefix(name, "claude_code.") || strings.HasPrefix(name, "codex.") || strings.HasPrefix(name, "gen_ai.") {
		return bounded(name, 128)
	}
	return fallback
}

func copyHexField(target, source map[string]any, key string, length int) {
	value, _ := source[key].(string)
	if len(value) != length {
		return
	}
	if _, err := hex.DecodeString(value); err == nil {
		target[key] = strings.ToLower(value)
	}
}

func copyDecimalField(target, source map[string]any, key string) {
	value, _ := source[key].(string)
	if value != "" && len(value) <= 24 && decimal(value) {
		target[key] = value
	}
}

func copyIntegerField(target, source map[string]any, key string, min, max int) {
	value, ok := source[key].(float64)
	if ok && value == float64(int(value)) && int(value) >= min && int(value) <= max {
		target[key] = int(value)
	}
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
