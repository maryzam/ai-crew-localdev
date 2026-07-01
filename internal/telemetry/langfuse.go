package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const otlpQueueSize = 128

var (
	otlpExportDeadline  = 2 * time.Second
	maxOTLPPayloadBytes = 1 << 20
)

var otlpWarnings io.Writer = os.Stderr
var newOTLPHTTPClient = func() *http.Client {
	return &http.Client{Timeout: otlpExportDeadline}
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

type OTLPConfig struct {
	Endpoint  string
	PublicKey string
	SecretKey string
}

type otlpPayload struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpWireAttribute `json:"attributes,omitempty"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type otlpSpan struct {
	Attributes        []otlpWireAttribute `json:"attributes,omitempty"`
	EndTimeUnixNano   string              `json:"endTimeUnixNano,omitempty"`
	Events            []otlpSpanEvent     `json:"events,omitempty"`
	Kind              int                 `json:"kind,omitempty"`
	Name              string              `json:"name,omitempty"`
	ParentSpanID      string              `json:"parentSpanId,omitempty"`
	SpanID            string              `json:"spanId,omitempty"`
	StartTimeUnixNano string              `json:"startTimeUnixNano,omitempty"`
	Status            otlpStatus          `json:"status,omitempty"`
	TraceID           string              `json:"traceId,omitempty"`
}

type otlpSpanEvent struct {
	Attributes   []otlpWireAttribute `json:"attributes,omitempty"`
	Name         string              `json:"name,omitempty"`
	TimeUnixNano string              `json:"timeUnixNano,omitempty"`
}

type otlpStatus struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type otlpWireAttribute struct {
	Key   string        `json:"key"`
	Value otlpWireValue `json:"value"`
}

type otlpWireValue struct {
	ArrayValue  *otlpArrayValue `json:"arrayValue,omitempty"`
	BoolValue   *bool           `json:"boolValue,omitempty"`
	DoubleValue *float64        `json:"doubleValue,omitempty"`
	IntValue    *string         `json:"intValue,omitempty"`
	StringValue *string         `json:"stringValue,omitempty"`
}

type otlpArrayValue struct {
	Values []otlpWireValue `json:"values"`
}

func newOTLPSink(config OTLPConfig) (*otlpSink, error) {
	if strings.TrimSpace(config.Endpoint) == "" {
		return nil, fmt.Errorf("OTLP endpoint must not be empty")
	}
	return &otlpSink{
		endpoint:  strings.TrimRight(strings.TrimSpace(config.Endpoint), "/"),
		publicKey: config.PublicKey,
		secretKey: config.SecretKey,
		client:    newOTLPHTTPClient(),
		events:    make([]Event, 0, 16),
		warnings:  otlpWarnings,
	}, nil
}

func normalizeTraceEndpoint(endpoint string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if strings.HasSuffix(endpoint, "/v1/traces") {
		return endpoint
	}
	return endpoint + "/v1/traces"
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
	if len(data) > maxOTLPPayloadBytes {
		return fmt.Errorf("OTLP: payload %d bytes exceeds budget %d", len(data), maxOTLPPayloadBytes)
	}
	return postOTLPJSONWithClient(s.client, OTLPConfig{
		Endpoint:  s.endpoint,
		PublicKey: s.publicKey,
		SecretKey: s.secretKey,
	}, s.headers, data)
}

func postOTLPJSON(config OTLPConfig, data []byte) error {
	return postOTLPJSONWithClient(newOTLPHTTPClient(), config, nil, data)
}

func postOTLPJSONWithClient(client *http.Client, config OTLPConfig, headers map[string]string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), otlpExportDeadline)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, normalizeTraceEndpoint(config.Endpoint), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("OTLP: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-langfuse-ingestion-version", "4")
	if config.PublicKey != "" && config.SecretKey != "" {
		request.SetBasicAuth(config.PublicKey, config.SecretKey)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("OTLP: export: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OTLP: export status %d", response.StatusCode)
	}
	return nil
}

func buildOTLPPayload(events []Event) (otlpPayload, error) {
	if len(events) == 0 {
		return otlpPayload{}, fmt.Errorf("OTLP: no events to export")
	}
	first := events[0]
	latest := events[len(events)-1]
	last := latest
	for _, event := range events {
		if event.EventType == "run.finished" {
			last = event
		}
	}
	last.Run = latest.Run
	rootAttributes := rootSpanAttributes(last)
	if len(rootAttributes) > MaxRootAttributes {
		return otlpPayload{}, fmt.Errorf("OTLP: root attribute budget exceeded: %d > %d", len(rootAttributes), MaxRootAttributes)
	}
	rootSpanID := spanID(first.Run.RunID, "root", 0)
	spans := []otlpSpan{{
		Attributes:        rootAttributes,
		EndTimeUnixNano:   strconv.FormatInt(last.Timestamp.UnixNano(), 10),
		Events:            rootSpanEvents(events),
		Kind:              1,
		Name:              "ai_agent.run",
		SpanID:            rootSpanID,
		StartTimeUnixNano: strconv.FormatInt(first.Timestamp.UnixNano(), 10),
		Status:            spanStatus(last.Outcome),
		TraceID:           first.Run.TraceID,
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
			return otlpPayload{}, fmt.Errorf("OTLP: child attribute budget exceeded: %d > %d", len(attributes), MaxChildAttributes)
		}
		start := event.Timestamp.Add(-time.Duration(event.DurationMS) * time.Millisecond)
		spans = append(spans, otlpSpan{
			Attributes:        attributes,
			EndTimeUnixNano:   strconv.FormatInt(event.Timestamp.UnixNano(), 10),
			Kind:              1,
			Name:              name,
			ParentSpanID:      rootSpanID,
			SpanID:            spanID(event.Run.RunID, name, childIndex),
			StartTimeUnixNano: strconv.FormatInt(start.UnixNano(), 10),
			Status:            spanStatus(event.Outcome),
			TraceID:           event.Run.TraceID,
		})
	}

	return otlpPayload{ResourceSpans: []otlpResourceSpans{{
		Resource: otlpResource{Attributes: resourceAttributes(last)},
		ScopeSpans: []otlpScopeSpans{{
			Scope: otlpScope{Name: "github.com/maryzam/ai-crew-localdev/internal/telemetry", Version: SchemaVersion},
			Spans: spans,
		}},
	}}}, nil
}

const destOTLP = "otlp"

const (
	sourceConstant = "constant"
	sourceRuntime  = "runtime"
)

type authorizedAttribute struct {
	key     string
	source  string
	extract func(Event) any
}

var resourceAttributesPolicy = []authorizedAttribute{
	{"service.name", sourceConstant, func(Event) any { return "ai-agent-launcher" }},
	{"service.namespace", sourceConstant, func(Event) any { return "ai-crew-localdev" }},
	{"service.version", sourceRuntime, func(e Event) any { return e.Run.Runtime.AIAgentVersion }},
	{"os.type", sourceRuntime, func(e Event) any { return e.Run.Runtime.OS }},
	{"host.arch", sourceRuntime, func(e Event) any { return e.Run.Runtime.Arch }},
	{"telemetry.sdk.language", sourceConstant, func(Event) any { return "go" }},
	{"telemetry.sdk.name", sourceConstant, func(Event) any { return "ai-agent-otlp-json" }},
}

var langfuseAttributesPolicy = []authorizedAttribute{
	{"langfuse.trace.name", sourceConstant, func(Event) any { return "ai-agent managed run" }},
	{"langfuse.trace.tags", "ai_agent.agent.type", func(e Event) any {
		return []string{"managed-run", runMode(e.Run.Task.Ref), e.Run.Agent.Type}
	}},
	{"langfuse.session.id", "ai_agent.task.ref", func(e Event) any { return e.Run.Task.Ref }},
}

func resourceAttributes(event Event) []otlpWireAttribute {
	return compactAttributes(buildAuthorizedAttributes(resourceAttributesPolicy, event))
}

func buildAuthorizedAttributes(table []authorizedAttribute, event Event) []otlpWireAttribute {
	attributes := make([]otlpWireAttribute, 0, len(table))
	for _, attr := range table {
		if !authorizedSource(attr.source) {
			continue
		}
		value := attr.extract(event)
		if value == nil {
			continue
		}
		attributes = append(attributes, newOTLPWireAttribute(attr.key, value))
	}
	return attributes
}

const (
	spanRoot uint8 = 1 << iota
	spanChild
)

type spanAttributeProjection struct {
	key     string
	spans   uint8
	extract func(Event) any
}

var spanAttributeProjections = []spanAttributeProjection{
	{"ai_agent.schema.version", spanRoot | spanChild, func(e Event) any { return e.Run.SchemaVersion }},
	{"ai_agent.run.mode", spanRoot | spanChild, func(e Event) any { return runMode(e.Run.Task.Ref) }},
	{"ai_agent.repository.slug", spanRoot | spanChild, func(e Event) any { return e.Run.Repository.Slug }},
	{"ai_agent.agent.type", spanRoot | spanChild, func(e Event) any { return e.Run.Agent.Type }},
	{"gen_ai.provider.name", spanRoot | spanChild, func(e Event) any { return e.Run.Model.Provider }},
	{"ai_agent.model.family", spanRoot | spanChild, func(e Event) any { return e.Run.Model.Family }},
	{"ai_agent.model.confidence", spanRoot | spanChild, func(e Event) any { return e.Run.Model.Resolution.Confidence }},
	{"ai_agent.task.ref", spanRoot | spanChild, func(e Event) any { return e.Run.Task.Ref }},
	{"ai_agent.exit_code", spanRoot | spanChild, exitCodeValue},

	{"ai_agent.run.outcome", spanRoot, func(e Event) any { return e.Run.Outcome }},
	{"ai_agent.run.id", spanRoot, func(e Event) any { return e.Run.RunID }},
	{"ai_agent.run.terminal_phase", spanRoot, func(e Event) any { return e.Run.TerminalPhase }},
	{"ai_agent.run.signal", spanRoot, func(e Event) any { return e.Run.Signal }},
	{"ai_agent.repository.commit", spanRoot, func(e Event) any { return e.Run.Repository.CommitSHA }},
	{"ai_agent.repository.branch", spanRoot, func(e Event) any { return e.Run.Repository.Branch }},
	{"ai_agent.repository.dirty", spanRoot, func(e Event) any { return e.Run.Repository.Dirty }},
	{"ai_agent.agent.identity", spanRoot, func(e Event) any { return e.Run.Agent.Identity }},
	{"ai_agent.agent.version", spanRoot, func(e Event) any { return e.Run.Agent.Version }},
	{"ai_agent.broker.session.id", spanRoot, func(e Event) any { return e.Run.Broker.SessionID }},
	{"ai_agent.verify.enabled", spanRoot, func(e Event) any { return e.Run.Execution.VerifyEnabled }},
	{"ai_agent.model.source", spanRoot, func(e Event) any { return e.Run.Model.Resolution.PrimarySource }},
	{"gen_ai.request.model", spanRoot, func(e Event) any { return e.Run.Model.Requested }},
	{"gen_ai.response.model", spanRoot, func(e Event) any { return e.Run.Model.Observed }},
	{"ai_agent.usage.status", spanRoot, func(e Event) any { return usageStatus(e.Run.Usage) }},
	{"ai_agent.usage.source", spanRoot, func(e Event) any { return usageString(e.Run.Usage, func(u *Usage) string { return u.Source }) }},
	{"ai_agent.usage.scope", spanRoot, func(e Event) any { return usageString(e.Run.Usage, func(u *Usage) string { return u.Scope }) }},
	{"ai_agent.usage.precision", spanRoot, func(e Event) any { return usageString(e.Run.Usage, func(u *Usage) string { return u.Precision }) }},
	{"ai_agent.usage.confidence", spanRoot, func(e Event) any { return usageString(e.Run.Usage, func(u *Usage) string { return u.Confidence }) }},
	{"gen_ai.usage.input_tokens", spanRoot, func(e Event) any { return usageToken(e.Run.Usage, func(u *Usage) *int64 { return u.InputTokens }) }},
	{"gen_ai.usage.output_tokens", spanRoot, func(e Event) any { return usageToken(e.Run.Usage, func(u *Usage) *int64 { return u.OutputTokens }) }},
	{"gen_ai.usage.cache_read.input_tokens", spanRoot, func(e Event) any { return usageToken(e.Run.Usage, func(u *Usage) *int64 { return u.CacheReadTokens }) }},
	{"ai_agent.usage.cache_write.input_tokens", spanRoot, func(e Event) any { return usageToken(e.Run.Usage, func(u *Usage) *int64 { return u.CacheWriteTokens }) }},
	{"gen_ai.usage.reasoning.output_tokens", spanRoot, func(e Event) any { return usageToken(e.Run.Usage, func(u *Usage) *int64 { return u.ReasoningTokens }) }},
	{"gen_ai.usage.total_tokens", spanRoot, func(e Event) any { return usageToken(e.Run.Usage, func(u *Usage) *int64 { return u.TotalTokens }) }},
	{"ai_agent.usage.cost.amount", spanRoot, func(e Event) any { return usageCostAmount(e.Run.Usage) }},
	{"ai_agent.usage.cost.currency", spanRoot, func(e Event) any { return usageCostCurrency(e.Run.Usage) }},

	{"ai_agent.attempt.outcome", spanChild, func(e Event) any { return e.Outcome }},
	{"ai_agent.attempt", spanChild, func(e Event) any { return int64(e.Attempt) }},
	{"ai_agent.command.sha256", spanChild, func(e Event) any { return e.Metadata["command_sha256"] }},
}

func rootSpanAttributes(event Event) []otlpWireAttribute { return spanAttributes(event, spanRoot) }

func childSpanAttributes(event Event) []otlpWireAttribute { return spanAttributes(event, spanChild) }

func spanAttributes(event Event, span uint8) []otlpWireAttribute {
	attributes := langfuseTraceAttributes(event)
	for _, field := range spanAttributeProjections {
		if field.spans&span == 0 || !exportAllowed(field.key) {
			continue
		}
		value := field.extract(event)
		if value == nil {
			continue
		}
		attributes = append(attributes, newOTLPWireAttribute(field.key, value))
	}
	return compactAttributes(attributes)
}

func langfuseTraceAttributes(event Event) []otlpWireAttribute {
	return buildAuthorizedAttributes(langfuseAttributesPolicy, event)
}

func exitCodeValue(event Event) any {
	if event.ExitCode == nil {
		return nil
	}
	return int64(*event.ExitCode)
}

func usageStatus(usage *Usage) any {
	if usage == nil {
		return nil
	}
	return usage.Status
}

func usageToken(usage *Usage, pick func(*Usage) *int64) any {
	if usage == nil {
		return nil
	}
	if value := pick(usage); value != nil {
		return *value
	}
	return nil
}

func authorizedSource(source string) bool {
	return source == sourceConstant || source == sourceRuntime || exportAllowed(source)
}

func exportAllowed(key string) bool {
	policy, ok := fieldPolicy(key)
	return ok && !policy.Sensitive && slicesContains(policy.Destinations, destOTLP)
}

type eventAttributeProjection struct {
	key     string
	extract func(Event) any
}

var eventAttributeProjections = []eventAttributeProjection{
	{"ai_agent.broker.session.id", func(e Event) any { return e.Run.Broker.SessionID }},
}

func rootSpanEvents(events []Event) []otlpSpanEvent {
	result := make([]otlpSpanEvent, 0, len(events))
	for _, event := range events {
		switch event.EventType {
		case "session.created", "session.revoked", "model.resolved", "usage.recorded":
			attributes := make([]otlpWireAttribute, 0, len(eventAttributeProjections))
			for _, field := range eventAttributeProjections {
				if !exportAllowed(field.key) {
					continue
				}
				value := field.extract(event)
				if value == nil {
					continue
				}
				attributes = append(attributes, newOTLPWireAttribute(field.key, value))
			}
			attributes = compactAttributes(attributes)
			if len(attributes) > MaxEventAttributes {
				continue
			}
			result = append(result, otlpSpanEvent{Attributes: attributes, Name: event.EventType, TimeUnixNano: strconv.FormatInt(event.Timestamp.UnixNano(), 10)})
		}
	}
	return result
}

func newOTLPWireAttribute(key string, value any) otlpWireAttribute {
	encoded := otlpWireValue{}
	switch typed := value.(type) {
	case string:
		if policy, ok := fieldPolicy(key); ok && policy.MaxLength > 0 {
			typed = bounded(typed, policy.MaxLength)
		}
		if key == "langfuse.session.id" {
			typed = bounded(typed, MaxPropagatedValueLength)
		}
		encoded.StringValue = &typed
	case bool:
		encoded.BoolValue = &typed
	case int64:
		value := strconv.FormatInt(typed, 10)
		encoded.IntValue = &value
	case []string:
		if len(typed) > MaxTagCount {
			typed = typed[:MaxTagCount]
		}
		values := make([]otlpWireValue, 0, len(typed))
		for _, item := range typed {
			value := bounded(item, MaxTagLength)
			values = append(values, otlpWireValue{StringValue: &value})
		}
		encoded.ArrayValue = &otlpArrayValue{Values: values}
	default:
		value := fmt.Sprint(value)
		encoded.StringValue = &value
	}
	return otlpWireAttribute{Key: key, Value: encoded}
}

func compactAttributes(attributes []otlpWireAttribute) []otlpWireAttribute {
	result := attributes[:0]
	for _, attribute := range attributes {
		if attribute.Value.StringValue != nil && *attribute.Value.StringValue == "" {
			continue
		}
		result = append(result, attribute)
	}
	return result
}

func spanStatus(outcome string) otlpStatus {
	if outcome == "" {
		return otlpStatus{}
	}
	if outcome == OutcomePassed || outcome == "passed" {
		return otlpStatus{Code: 1}
	}
	return otlpStatus{Code: 2, Message: outcome}
}

func spanID(runID, name string, index int) string {
	return sha256Hex(runID + ":" + name + ":" + strconv.Itoa(index))[:16]
}
