package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const otlpQueueSize = 128

var otlpWarnings io.Writer = os.Stderr
var newOTLPHTTPClient = func() *http.Client {
	return &http.Client{Timeout: 2 * time.Second}
}

type otlpSink struct {
	endpoint  string
	publicKey string
	secretKey string
	headers   map[string]string
	client    *http.Client
	mu        sync.Mutex
	events    []Event
	warnOnce  sync.Once
	closeOnce sync.Once
	closed    bool
	warnings  io.Writer
}

func newOTLPSinkFromEnv() *otlpSink {
	endpoint := traceEndpointFromEnv()
	publicKey := firstEnv("AI_AGENT_LANGFUSE_PUBLIC_KEY", "LANGFUSE_PUBLIC_KEY")
	secretKey := firstEnv("AI_AGENT_LANGFUSE_SECRET_KEY", "LANGFUSE_SECRET_KEY")
	if endpoint == "" && publicKey != "" && secretKey != "" {
		host := firstEnv("AI_AGENT_LANGFUSE_HOST", "LANGFUSE_HOST")
		if host == "" {
			host = defaultLangfuseHost
		}
		endpoint = strings.TrimRight(host, "/") + "/api/public/otel/v1/traces"
	}
	if endpoint == "" {
		return nil
	}
	return &otlpSink{
		endpoint:  endpoint,
		publicKey: publicKey,
		secretKey: secretKey,
		headers:   parseOTLPHeaders(firstEnv("AI_AGENT_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_HEADERS")),
		client:    newOTLPHTTPClient(),
		events:    make([]Event, 0, 16),
		warnings:  otlpWarnings,
	}
}

func traceEndpointFromEnv() string {
	if endpoint := strings.TrimSpace(os.Getenv("AI_AGENT_OTLP_TRACES_ENDPOINT")); endpoint != "" {
		return normalizeTraceEndpoint(endpoint)
	}
	if endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")); endpoint != "" {
		return endpoint
	}
	if endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")); endpoint != "" {
		return normalizeTraceEndpoint(endpoint)
	}
	return ""
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func normalizeTraceEndpoint(endpoint string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if strings.HasSuffix(endpoint, "/v1/traces") {
		return endpoint
	}
	return endpoint + "/v1/traces"
}

func parseOTLPHeaders(raw string) map[string]string {
	result := make(map[string]string)
	for _, item := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok || key == "" {
			continue
		}
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		result[key] = value
	}
	return result
}

func (s *otlpSink) enqueue(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if len(s.events) >= otlpQueueSize {
		s.warn(fmt.Errorf("OTLP telemetry queue full; dropping event %s", event.EventType))
		if event.EventType == "run.finished" && len(s.events) > 1 {
			s.events[len(s.events)-1] = event
		}
		return
	}
	s.events = append(s.events, event)
}

func (s *otlpSink) close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		events := append([]Event(nil), s.events...)
		s.mu.Unlock()
		if len(events) == 0 {
			return
		}
		if err := s.ingest(events); err != nil {
			s.warn(err)
		}
	})
}

func (s *otlpSink) warn(err error) {
	s.warnOnce.Do(func() {
		_, _ = fmt.Fprintf(s.warnings, "warning: OTLP telemetry export failed: %v\n", err)
	})
}

func (s *otlpSink) ingest(events []Event) error {
	payload, err := buildOTLPPayload(events)
	if err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("OTLP: marshal payload: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("OTLP: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-langfuse-ingestion-version", "4")
	if s.publicKey != "" && s.secretKey != "" {
		request.SetBasicAuth(s.publicKey, s.secretKey)
	}
	for key, value := range s.headers {
		request.Header.Set(key, value)
	}

	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("OTLP: export: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OTLP: export status %d", response.StatusCode)
	}
	return nil
}

func buildOTLPPayload(events []Event) (map[string]any, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("OTLP: no events to export")
	}
	first := events[0]
	last := events[len(events)-1]
	var recordedUsage *Usage
	for _, event := range events {
		if event.EventType == "run.finished" {
			last = event
		}
		if event.Usage != nil {
			recordedUsage = cloneUsage(event.Usage)
		}
	}
	last.Usage = recordedUsage
	rootAttributes := rootSpanAttributes(last)
	if len(rootAttributes) > MaxRootAttributes {
		return nil, fmt.Errorf("OTLP: root attribute budget exceeded: %d > %d", len(rootAttributes), MaxRootAttributes)
	}
	rootSpanID := spanID(first.RunID, "root", 0)
	spans := []any{map[string]any{
		"traceId":           first.TraceID,
		"spanId":            rootSpanID,
		"name":              "ai_agent.run",
		"kind":              1,
		"startTimeUnixNano": strconv.FormatInt(first.Timestamp.UnixNano(), 10),
		"endTimeUnixNano":   strconv.FormatInt(last.Timestamp.UnixNano(), 10),
		"attributes":        rootAttributes,
		"events":            rootSpanEvents(events),
		"status":            spanStatus(last.Outcome),
	}}
	childIndex := 0
	for _, event := range events {
		name := ""
		switch event.EventType {
		case "agent.command.finished":
			name = "agent.command"
		case "verify.attempt.finished", "verify.command.finished":
			name = "verify.attempt"
		default:
			continue
		}
		childIndex++
		attributes := childSpanAttributes(event)
		if len(attributes) > MaxChildAttributes {
			return nil, fmt.Errorf("OTLP: child attribute budget exceeded: %d > %d", len(attributes), MaxChildAttributes)
		}
		start := event.Timestamp.Add(-time.Duration(event.DurationMS) * time.Millisecond)
		spans = append(spans, map[string]any{
			"traceId":           event.TraceID,
			"spanId":            spanID(event.RunID, name, childIndex),
			"parentSpanId":      rootSpanID,
			"name":              name,
			"kind":              1,
			"startTimeUnixNano": strconv.FormatInt(start.UnixNano(), 10),
			"endTimeUnixNano":   strconv.FormatInt(event.Timestamp.UnixNano(), 10),
			"attributes":        attributes,
			"status":            spanStatus(event.Outcome),
		})
	}

	return map[string]any{
		"resourceSpans": []any{map[string]any{
			"resource": map[string]any{"attributes": resourceAttributes(last)},
			"scopeSpans": []any{map[string]any{
				"scope": map[string]any{"name": "github.com/maryzam/ai-crew-localdev/internal/telemetry", "version": SchemaVersion},
				"spans": spans,
			}},
		}},
	}, nil
}

func resourceAttributes(event Event) []any {
	return compactAttributes([]any{
		otlpAttribute("service.name", "ai-agent-launcher"),
		otlpAttribute("service.namespace", "ai-crew-localdev"),
		otlpAttribute("service.version", event.Runtime.AIAgentVersion),
		otlpAttribute("os.type", event.Runtime.OS),
		otlpAttribute("host.arch", event.Runtime.Arch),
		otlpAttribute("telemetry.sdk.language", "go"),
		otlpAttribute("telemetry.sdk.name", "ai-agent-otlp-json"),
	})
}

func rootSpanAttributes(event Event) []any {
	attributes := propagatedAttributes(event)
	attributes = append(attributes,
		otlpAttribute("ai_agent.run.id", event.RunID),
		otlpAttribute("ai_agent.run.outcome", event.Outcome),
		otlpAttribute("ai_agent.run.terminal_phase", event.Phase),
		otlpAttribute("ai_agent.repository.commit", event.Repository.CommitSHA),
		otlpAttribute("ai_agent.repository.branch", event.Repository.Branch),
		otlpAttribute("ai_agent.repository.dirty", event.Repository.Dirty),
		otlpAttribute("ai_agent.agent.identity", event.Agent.Identity),
		otlpAttribute("ai_agent.broker.session.id", event.SessionID),
		otlpAttribute("ai_agent.verify.enabled", event.VerifyEnabled),
		otlpAttribute("ai_agent.model.source", event.Model.Resolution.PrimarySource),
	)
	if event.Model.Requested != "" {
		attributes = append(attributes, otlpAttribute("gen_ai.request.model", event.Model.Requested))
	}
	if event.Model.Observed != "" {
		attributes = append(attributes, otlpAttribute("gen_ai.response.model", event.Model.Observed))
	}
	if event.ExitCode != nil {
		attributes = append(attributes, otlpAttribute("ai_agent.exit_code", int64(*event.ExitCode)))
	}
	if event.Signal != "" {
		attributes = append(attributes, otlpAttribute("ai_agent.run.signal", event.Signal))
	}
	if event.Usage != nil {
		attributes = append(attributes, usageAttributes(event.Usage)...)
	}
	return compactAttributes(attributes)
}

func propagatedAttributes(event Event) []any {
	attributes := []any{
		otlpAttribute("langfuse.trace.name", "ai-agent managed run"),
		otlpAttribute("langfuse.trace.tags", []string{"managed-run", runMode(event.Task.Ref), event.Agent.Type}),
		otlpAttribute("langfuse.trace.metadata.schemaversion", event.SchemaVersion),
		otlpAttribute("langfuse.trace.metadata.repo", event.Repository.Slug),
		otlpAttribute("langfuse.trace.metadata.agent", event.Agent.Type),
		otlpAttribute("langfuse.trace.metadata.provider", event.Model.Provider),
		otlpAttribute("langfuse.trace.metadata.modelfamily", event.Model.Family),
		otlpAttribute("langfuse.trace.metadata.mode", runMode(event.Task.Ref)),
		otlpAttribute("langfuse.trace.metadata.tasktype", event.Task.Type),
		otlpAttribute("ai_agent.schema.version", event.SchemaVersion),
		otlpAttribute("ai_agent.run.mode", runMode(event.Task.Ref)),
		otlpAttribute("ai_agent.repository.slug", event.Repository.Slug),
		otlpAttribute("ai_agent.agent.type", event.Agent.Type),
		otlpAttribute("gen_ai.provider.name", event.Model.Provider),
		otlpAttribute("ai_agent.model.family", event.Model.Family),
		otlpAttribute("ai_agent.model.confidence", event.Model.Resolution.Confidence),
	}
	if event.Task.Ref != "" {
		attributes = append(attributes,
			otlpAttribute("langfuse.session.id", event.Task.Ref),
			otlpAttribute("ai_agent.task.ref", event.Task.Ref),
		)
	}
	return compactAttributes(attributes)
}

func childSpanAttributes(event Event) []any {
	attributes := propagatedAttributes(event)
	attributes = append(attributes,
		otlpAttribute("ai_agent.attempt", int64(event.Attempt)),
		otlpAttribute("ai_agent.run.outcome", event.Outcome),
	)
	if event.ExitCode != nil {
		attributes = append(attributes, otlpAttribute("ai_agent.exit_code", int64(*event.ExitCode)))
	}
	if hash := event.Metadata["command_sha256"]; hash != "" {
		attributes = append(attributes, otlpAttribute("ai_agent.command.sha256", hash))
	}
	return compactAttributes(attributes)
}

func rootSpanEvents(events []Event) []any {
	result := make([]any, 0, len(events))
	for _, event := range events {
		switch event.EventType {
		case "session.created", "session.revoked", "model.resolved", "usage.recorded":
			attributes := []any{otlpAttribute("ai_agent.run.outcome", event.Outcome)}
			if event.SessionID != "" {
				attributes = append(attributes, otlpAttribute("ai_agent.broker.session.id", event.SessionID))
			}
			attributes = compactAttributes(attributes)
			if len(attributes) > MaxEventAttributes {
				continue
			}
			result = append(result, map[string]any{
				"timeUnixNano": strconv.FormatInt(event.Timestamp.UnixNano(), 10),
				"name":         event.EventType,
				"attributes":   attributes,
			})
		}
	}
	return result
}

func usageAttributes(usage *Usage) []any {
	attributes := []any{otlpAttribute("ai_agent.usage.status", usage.Status)}
	for key, value := range map[string]*int64{
		"gen_ai.usage.input_tokens":               usage.InputTokens,
		"gen_ai.usage.output_tokens":              usage.OutputTokens,
		"gen_ai.usage.cache_read.input_tokens":    usage.CacheReadTokens,
		"ai_agent.usage.cache_write.input_tokens": usage.CacheWriteTokens,
		"gen_ai.usage.reasoning.output_tokens":    usage.ReasoningTokens,
		"gen_ai.usage.total_tokens":               usage.TotalTokens,
	} {
		if value != nil {
			attributes = append(attributes, otlpAttribute(key, *value))
		}
	}
	if usage.CostAmount != nil {
		attributes = append(attributes, otlpAttribute("ai_agent.usage.cost.amount", *usage.CostAmount))
	}
	if usage.CostCurrency != "" {
		attributes = append(attributes, otlpAttribute("ai_agent.usage.cost.currency", usage.CostCurrency))
	}
	return attributes
}

func otlpAttribute(key string, value any) map[string]any {
	var encoded map[string]any
	switch typed := value.(type) {
	case string:
		if policy, ok := fieldPolicy(key); ok && policy.MaxLength > 0 {
			typed = bounded(typed, policy.MaxLength)
		}
		if strings.HasPrefix(key, "langfuse.trace.metadata.") || key == "langfuse.session.id" {
			typed = bounded(typed, MaxPropagatedValueLength)
		}
		encoded = map[string]any{"stringValue": typed}
	case bool:
		encoded = map[string]any{"boolValue": typed}
	case int64:
		encoded = map[string]any{"intValue": strconv.FormatInt(typed, 10)}
	case []string:
		if len(typed) > MaxTagCount {
			typed = typed[:MaxTagCount]
		}
		values := make([]any, 0, len(typed))
		for _, item := range typed {
			values = append(values, map[string]any{"stringValue": bounded(item, MaxTagLength)})
		}
		encoded = map[string]any{"arrayValue": map[string]any{"values": values}}
	default:
		encoded = map[string]any{"stringValue": fmt.Sprint(value)}
	}
	return map[string]any{"key": key, "value": encoded}
}

func compactAttributes(attributes []any) []any {
	result := attributes[:0]
	for _, attribute := range attributes {
		item := attribute.(map[string]any)
		value := item["value"].(map[string]any)
		if stringValue, ok := value["stringValue"].(string); ok && stringValue == "" {
			continue
		}
		result = append(result, attribute)
	}
	return result
}

func spanStatus(outcome string) map[string]any {
	if outcome == "" {
		return map[string]any{}
	}
	if outcome == OutcomePassed || outcome == "passed" {
		return map[string]any{"code": 1}
	}
	return map[string]any{"code": 2, "message": outcome}
}

func spanID(runID, name string, index int) string {
	return sha256Hex(runID + ":" + name + ":" + strconv.Itoa(index))[:16]
}
