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

type OTLPConfig struct {
	Endpoint  string
	PublicKey string
	SecretKey string
}

func newOTLPSink(config OTLPConfig) (*otlpSink, error) {
	if strings.TrimSpace(config.Endpoint) == "" {
		return nil, fmt.Errorf("OTLP endpoint must not be empty")
	}
	return &otlpSink{
		endpoint:  normalizeTraceEndpoint(config.Endpoint),
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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

func buildOTLPPayload(events []Event) (map[string]any, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("OTLP: no events to export")
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
		return nil, fmt.Errorf("OTLP: root attribute budget exceeded: %d > %d", len(rootAttributes), MaxRootAttributes)
	}
	rootSpanID := spanID(first.Run.RunID, "root", 0)
	spans := []any{map[string]any{
		"traceId":           first.Run.TraceID,
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
			"traceId":           event.Run.TraceID,
			"spanId":            spanID(event.Run.RunID, name, childIndex),
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

// staticAttr projects an OTLP attribute that is not a schema field: resource
// descriptors and Langfuse projection hints. Like otlpFields it is a single
// source of truth, so validateStaticExports can assert every exported key is
// either a non-sensitive schema field or an allowlisted export namespace,
// extending the privacy-by-construction guarantee to attributes that do not
// flow through the field registry.
type staticAttr struct {
	key     string
	extract func(Event) any
}

var resourceAttrs = []staticAttr{
	{"service.name", func(Event) any { return "ai-agent-launcher" }},
	{"service.namespace", func(Event) any { return "ai-crew-localdev" }},
	{"service.version", func(e Event) any { return e.Run.Runtime.AIAgentVersion }},
	{"os.type", func(e Event) any { return e.Run.Runtime.OS }},
	{"host.arch", func(e Event) any { return e.Run.Runtime.Arch }},
	{"telemetry.sdk.language", func(Event) any { return "go" }},
	{"telemetry.sdk.name", func(Event) any { return "ai-agent-otlp-json" }},
}

// langfuseHints carry Langfuse-specific projection hints. These are not schema
// fields, so they stay under staticExportPrefixes rather than the field registry.
var langfuseHints = []staticAttr{
	{"langfuse.trace.name", func(Event) any { return "ai-agent managed run" }},
	{"langfuse.trace.tags", func(e Event) any {
		return []string{"managed-run", runMode(e.Run.Task.Ref), e.Run.Agent.Type}
	}},
	{"langfuse.trace.metadata.schemaversion", func(e Event) any { return e.Run.SchemaVersion }},
	{"langfuse.trace.metadata.repo", func(e Event) any { return e.Run.Repository.Slug }},
	{"langfuse.trace.metadata.agent", func(e Event) any { return e.Run.Agent.Type }},
	{"langfuse.trace.metadata.provider", func(e Event) any { return e.Run.Model.Provider }},
	{"langfuse.trace.metadata.modelfamily", func(e Event) any { return e.Run.Model.Family }},
	{"langfuse.trace.metadata.mode", func(e Event) any { return runMode(e.Run.Task.Ref) }},
	{"langfuse.trace.metadata.tasktype", func(e Event) any { return e.Run.Task.Type }},
	{"langfuse.session.id", func(e Event) any { return e.Run.Task.Ref }},
}

// staticExportPrefixes are the only non-policy attribute namespaces permitted to
// leave the host. validateStaticExports rejects any static export key outside
// this set that is not backed by a non-sensitive field policy.
var staticExportPrefixes = []string{"langfuse.", "service.", "os.", "host.", "telemetry.sdk."}

func resourceAttributes(event Event) []any {
	return compactAttributes(buildStaticAttributes(resourceAttrs, event))
}

func buildStaticAttributes(table []staticAttr, event Event) []any {
	attributes := make([]any, 0, len(table))
	for _, attr := range table {
		value := attr.extract(event)
		if value == nil {
			continue
		}
		attributes = append(attributes, otlpAttribute(attr.key, value))
	}
	return attributes
}

// Span placement flags for OTLP projection.
const (
	spanRoot uint8 = 1 << iota
	spanChild
)

// spanField projects one schema field onto OTLP spans. The extractor returns
// nil to omit the attribute (e.g. an absent exit code) and "" for string values
// that compaction drops. otlpFields is the single source of truth for which
// schema fields reach OTLP: a field with no entry here is never exported, and
// validateOTLPProjection asserts every entry maps to an otlp-allowed,
// non-sensitive policy, so local-only fields cannot leak by construction.
type spanField struct {
	key     string
	spans   uint8
	extract func(Event) any
}

// Run-level attributes read from the event's run snapshot (e.Run); per-event
// attributes (outcome, exit_code, attempt, command hash) read the envelope.
var otlpFields = []spanField{
	// Propagated context: present on the root and every child span for grouping.
	{"ai_agent.schema.version", spanRoot | spanChild, func(e Event) any { return e.Run.SchemaVersion }},
	{"ai_agent.run.mode", spanRoot | spanChild, func(e Event) any { return runMode(e.Run.Task.Ref) }},
	{"ai_agent.repository.slug", spanRoot | spanChild, func(e Event) any { return e.Run.Repository.Slug }},
	{"ai_agent.agent.type", spanRoot | spanChild, func(e Event) any { return e.Run.Agent.Type }},
	{"gen_ai.provider.name", spanRoot | spanChild, func(e Event) any { return e.Run.Model.Provider }},
	{"ai_agent.model.family", spanRoot | spanChild, func(e Event) any { return e.Run.Model.Family }},
	{"ai_agent.model.confidence", spanRoot | spanChild, func(e Event) any { return e.Run.Model.Resolution.Confidence }},
	{"ai_agent.task.ref", spanRoot | spanChild, func(e Event) any { return e.Run.Task.Ref }},
	{"ai_agent.exit_code", spanRoot | spanChild, exitCodeValue},

	// Root-only run-level attributes. ai_agent.run.outcome is the run's terminal
	// outcome read from the run snapshot, so it stays root-only; child spans carry
	// their own per-attempt outcome below to avoid labeling a passed agent command
	// with a verify_failed run outcome.
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

	// Child-only attributes. ai_agent.attempt.outcome is the per-attempt outcome
	// from the event envelope, kept distinct from the root run outcome above.
	{"ai_agent.attempt.outcome", spanChild, func(e Event) any { return e.Outcome }},
	{"ai_agent.attempt", spanChild, func(e Event) any { return int64(e.Attempt) }},
	{"ai_agent.command.sha256", spanChild, func(e Event) any { return e.Metadata["command_sha256"] }},
}

func rootSpanAttributes(event Event) []any { return spanAttributes(event, spanRoot) }

func childSpanAttributes(event Event) []any { return spanAttributes(event, spanChild) }

func spanAttributes(event Event, span uint8) []any {
	attributes := langfuseTraceAttributes(event)
	for _, field := range otlpFields {
		if field.spans&span == 0 {
			continue
		}
		value := field.extract(event)
		if value == nil {
			continue
		}
		attributes = append(attributes, otlpAttribute(field.key, value))
	}
	return compactAttributes(attributes)
}

func langfuseTraceAttributes(event Event) []any {
	return buildStaticAttributes(langfuseHints, event)
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

func usageString(usage *Usage, pick func(*Usage) string) any {
	if usage == nil {
		return nil
	}
	return pick(usage)
}

func usageCostAmount(usage *Usage) any {
	if usage == nil || usage.CostAmount == nil {
		return nil
	}
	return *usage.CostAmount
}

func usageCostCurrency(usage *Usage) any {
	if usage == nil {
		return nil
	}
	return usage.CostCurrency
}

// validateOTLPProjection asserts the projection table and the schema registry
// agree in both directions, enforcing the privacy boundary structurally rather
// than by test convention. Every projected field must map to an otlp-allowed,
// non-sensitive policy, and every otlp-allowed policy must have exactly one
// projection, so a field declared exportable can never silently lack a span
// mapping (or gain a duplicate one).
func validateOTLPProjection() error {
	projected := make(map[string]int, len(otlpFields))
	for _, field := range otlpFields {
		policy, ok := fieldPolicy(field.key)
		if !ok {
			return fmt.Errorf("OTLP projection field %q has no schema policy", field.key)
		}
		if !slicesContains(policy.Destinations, "otlp") {
			return fmt.Errorf("OTLP projection field %q is not allowed to export (destinations %v)", field.key, policy.Destinations)
		}
		if policy.Sensitive {
			return fmt.Errorf("sensitive field %q must not be projected to OTLP", field.key)
		}
		if field.spans == 0 || field.extract == nil {
			return fmt.Errorf("OTLP projection field %q needs a span placement and extractor", field.key)
		}
		projected[field.key]++
	}
	for _, policy := range FieldPolicies {
		if !slicesContains(policy.Destinations, "otlp") {
			continue
		}
		switch projected[policy.Key] {
		case 1:
		case 0:
			return fmt.Errorf("schema field %q is OTLP-capable but has no projection entry", policy.Key)
		default:
			return fmt.Errorf("schema field %q has %d OTLP projections; expected exactly one", policy.Key, projected[policy.Key])
		}
	}
	return validateStaticExports()
}

// validateStaticExports brings resource attributes and Langfuse hints under the
// same boundary as schema fields: each static key must be either a non-sensitive
// field policy or fall under an allowlisted export namespace, so a future static
// attribute cannot sidestep the privacy boundary the field registry enforces.
func validateStaticExports() error {
	for _, attr := range append(append([]staticAttr(nil), resourceAttrs...), langfuseHints...) {
		if attr.extract == nil {
			return fmt.Errorf("static export %q needs an extractor", attr.key)
		}
		if policy, ok := fieldPolicy(attr.key); ok {
			if policy.Sensitive {
				return fmt.Errorf("static export %q maps to a sensitive field policy", attr.key)
			}
			continue
		}
		if !hasStaticExportPrefix(attr.key) {
			return fmt.Errorf("static export %q is neither a field policy nor an allowed export namespace %v", attr.key, staticExportPrefixes)
		}
	}
	return nil
}

func hasStaticExportPrefix(key string) bool {
	for _, prefix := range staticExportPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// rootSpanEvents projects lifecycle markers onto the root span. They carry the
// broker session id for correlation only; the run outcome belongs to the root
// span attributes, not to these point-in-time events.
func rootSpanEvents(events []Event) []any {
	result := make([]any, 0, len(events))
	for _, event := range events {
		switch event.EventType {
		case "session.created", "session.revoked", "model.resolved", "usage.recorded":
			var attributes []any
			if event.Run.Broker.SessionID != "" {
				attributes = append(attributes, otlpAttribute("ai_agent.broker.session.id", event.Run.Broker.SessionID))
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
