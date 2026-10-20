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
	"sync/atomic"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/runevents"
)

const nativeRequestLimit = 32 << 20

type NativeExporter interface {
	Export(string, []byte) error
}

type NativeRelayStats struct {
	Rejected uint64
	Failed   uint64
}

type NativeRelay struct {
	recorder  *Recorder
	exporter  NativeExporter
	server    *http.Server
	listener  net.Listener
	token     string
	handlers  sync.RWMutex
	closing   bool
	closeOnce sync.Once
	warnOnce  sync.Once
	rejected  atomic.Uint64
	failed    atomic.Uint64
	usage     runevents.NativeUsageAccumulator
	observer  func(runevents.NativeUsage)
}

type NativeRelayOption func(*NativeRelay)

func WithNativeUsageObserver(observer func(runevents.NativeUsage)) NativeRelayOption {
	return func(relay *NativeRelay) {
		relay.observer = observer
	}
}

func StartNativeRelay(recorder *Recorder, exporter NativeExporter, options ...NativeRelayOption) (*NativeRelay, error) {
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
	relay := &NativeRelay{
		recorder: recorder,
		exporter: exporter,
		listener: listener,
		token:    hex.EncodeToString(tokenBytes),
	}
	for _, option := range options {
		if option != nil {
			option(relay)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", relay.handleLogs)
	mux.HandleFunc("/v1/traces", relay.handleTraces)
	mux.HandleFunc("/v1/metrics", relay.handleMetrics)
	relay.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       5 * time.Second,
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

func (r *NativeRelay) Close() NativeRelayStats {
	r.closeOnce.Do(func() {
		r.handlers.Lock()
		r.closing = true
		r.handlers.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := r.server.Shutdown(ctx); err != nil {
			_ = r.server.Close()
		}
		cancel()
		r.recordUsage()
	})
	return NativeRelayStats{Rejected: r.rejected.Load(), Failed: r.failed.Load()}
}

func (r *NativeRelay) handleLogs(w http.ResponseWriter, request *http.Request) {
	r.handleSignal(w, request, "logs")
}

func (r *NativeRelay) handleTraces(w http.ResponseWriter, request *http.Request) {
	r.handleSignal(w, request, "traces")
}

func (r *NativeRelay) handleMetrics(w http.ResponseWriter, request *http.Request) {
	r.handleSignal(w, request, "metrics")
}

func (r *NativeRelay) handleSignal(w http.ResponseWriter, request *http.Request, signal string) {
	r.handlers.RLock()
	defer r.handlers.RUnlock()
	if r.closing {
		r.rejected.Add(1)
		http.Error(w, "relay closing", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost || !r.authorized(request.Header.Get("Authorization")) {
		r.rejected.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, nativeRequestLimit+1))
	if err != nil || len(body) > nativeRequestLimit {
		r.rejected.Add(1)
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		r.rejected.Add(1)
		http.Error(w, "invalid OTLP JSON", http.StatusBadRequest)
		return
	}
	if r.exporter != nil {
		if err := r.exporter.Export(signal, body); err != nil {
			r.failed.Add(1)
			r.warn(err)
			http.Error(w, "telemetry was not stored", http.StatusServiceUnavailable)
			return
		}
	}
	if signal == "logs" {
		r.collectUsage(payload)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{}`)
}

func (r *NativeRelay) authorized(value string) bool {
	want := []byte("Bearer " + r.token)
	got := []byte(value)
	return len(got) == len(want) && subtle.ConstantTimeCompare(got, want) == 1
}

func (r *NativeRelay) warn(err error) {
	r.warnOnce.Do(func() {
		_, _ = fmt.Fprintf(localWarnings, "warning: native OTLP telemetry failed: %v\n", err)
	})
}

func (r *NativeRelay) collectUsage(payload any) {
	walkMaps(payload, func(item map[string]any) {
		attributes := decodeOTLPAttributes(item["attributes"])
		eventName := stringAttribute(attributes, "event.name")
		if eventName == "" {
			eventName = otlpString(item["body"])
		}
		usage, ok := nativeUsageFromEvent(eventName, attributes)
		if ok {
			r.usage.Add(usage)
			if r.observer != nil {
				r.observer(usage)
			}
		}
	})
}

func (r *NativeRelay) recordUsage() {
	usage := r.usage.Snapshot()
	if !usage.Recorded {
		return
	}
	if usage.Model != "" && !usage.ModelMixed {
		r.recorder.ObserveModel(usage.Model, "", "native_otel")
	}
	r.recorder.RecordUsage(usage.RunUsage())
}

func nativeUsageFromEvent(eventName string, attributes map[string]any) (runevents.NativeUsage, bool) {
	var usage runevents.NativeUsage
	switch eventName {
	case "claude_code.api_request":
		usage.Input = intAttribute(attributes, "input_tokens")
		usage.Output = intAttribute(attributes, "output_tokens")
		usage.CacheRead = intAttribute(attributes, "cache_read_tokens")
		usage.CacheWrite = intAttribute(attributes, "cache_creation_tokens")
		usage.Total = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
		usage.CostUSD = floatAttribute(attributes, "cost_usd")
	case "codex.sse_event":
		if stringAttribute(attributes, "event.kind") != "response.completed" {
			return runevents.NativeUsage{}, false
		}
		usage.Input = intAttribute(attributes, "input_token_count")
		usage.Output = intAttribute(attributes, "output_token_count")
		usage.CacheRead = intAttribute(attributes, "cached_token_count")
		usage.Reasoning = intAttribute(attributes, "reasoning_token_count")
		usage.Total = usage.Input + usage.Output
	default:
		return runevents.NativeUsage{}, false
	}
	if usage.Total <= 0 {
		return runevents.NativeUsage{}, false
	}
	usage.Model = stringAttribute(attributes, "model")
	usage.Recorded = true
	return usage, true
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
