package telemetry

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/platform/correlation"
	"github.com/maryzam/ai-crew-localdev/internal/platform/modelattrib"
	"github.com/maryzam/ai-crew-localdev/internal/platform/runevents"
)

const (
	SchemaVersion            = runevents.SchemaVersion
	MaxRootAttributes        = 48
	MaxChildAttributes       = 24
	MaxEventAttributes       = 12
	MaxPropagatedValueLength = modelattrib.MaxPropagatedValueLength
	MaxSessionIDLength       = correlation.MaxTaskRefLength
	MaxTagCount              = 8
	MaxTagLength             = 64
)

const (
	MaxOTLPExportPayloadBytes = 1 << 20
	MaxExportResourceSpans    = 8
	MaxExportScopeSpans       = 8
	MaxExportSpans            = 128
	MaxExportSpanEvents       = 32
	MaxExportNameLength       = 128
)

const (
	nativeScopeName     = "ai-agent-native"
	summaryScopeName    = "github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
	nativeSpanFallback  = "agent.operation"
	nativeEventFallback = "agent.event"
)

func allowedExportScope(name string) bool {
	return name == nativeScopeName || name == summaryScopeName
}

func allowedExportSpanName(name string) bool {
	switch name {
	case "ai_agent.run", "agent.command", "verify.attempt", nativeSpanFallback:
		return true
	default:
		return exportNameHasAllowedPrefix(name)
	}
}

func allowedExportEventName(name string) bool {
	switch name {
	case "session.created", "session.revoked", "model.resolved", "usage.recorded", nativeEventFallback:
		return true
	default:
		return exportNameHasAllowedPrefix(name)
	}
}

func exportNameHasAllowedPrefix(name string) bool {
	if len(name) > MaxExportNameLength {
		return false
	}
	return strings.HasPrefix(name, "claude_code.") || strings.HasPrefix(name, "codex.") || strings.HasPrefix(name, "gen_ai.")
}

type Cardinality string

type FieldID string

const (
	CardinalityLow       Cardinality = "low"
	CardinalityWorkspace Cardinality = "workspace"
	CardinalityHigh      Cardinality = "high"
	CardinalityUnbounded Cardinality = "unbounded"
)

type FieldPolicy struct {
	Key          FieldID
	Scope        string
	Destinations []string
	Cardinality  Cardinality
	MaxLength    int
	Sensitive    bool
	Metric       bool
	NativeInput  bool
}

var fieldRegistry = []FieldPolicy{
	{Key: "ai_agent.schema.version", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 16, Metric: true},
	{Key: "ai_agent.run.id", Scope: "trace", Destinations: []string{"local", "otlp", "broker", "environment"}, Cardinality: CardinalityHigh, MaxLength: 64},
	{Key: "ai_agent.run.mode", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 16, Metric: true},
	{Key: "ai_agent.run.outcome", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 32, Metric: true},
	{Key: "ai_agent.run.terminal_phase", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 32, Metric: true},
	{Key: "ai_agent.run.signal", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, MaxLength: 32},
	{Key: "ai_agent.task.ref", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse", "broker", "environment"}, Cardinality: CardinalityHigh, MaxLength: MaxSessionIDLength},
	{Key: "ai_agent.repository.slug", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityWorkspace, MaxLength: MaxPropagatedValueLength},
	{Key: "ai_agent.repository.commit", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh, MaxLength: 64},
	{Key: "ai_agent.repository.branch", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh, MaxLength: MaxPropagatedValueLength},
	{Key: "ai_agent.repository.dirty", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, Metric: true},
	{Key: "ai_agent.repository.root_path", Scope: "local", Destinations: []string{"local"}, Cardinality: CardinalityUnbounded, MaxLength: 4096, Sensitive: true},
	{Key: "ai_agent.agent.type", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 32, Metric: true},
	{Key: "ai_agent.agent.identity", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityWorkspace, MaxLength: 128},
	{Key: "ai_agent.agent.version", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityWorkspace, MaxLength: 64},
	{Key: "gen_ai.provider.name", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 64, Metric: true},
	{Key: "gen_ai.request.model", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityHigh, MaxLength: MaxPropagatedValueLength, NativeInput: true},
	{Key: "gen_ai.response.model", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityHigh, MaxLength: MaxPropagatedValueLength},
	{Key: "ai_agent.model.family", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 64, Metric: true},
	{Key: "ai_agent.model.confidence", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 16, Metric: true},
	{Key: "ai_agent.model.source", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, MaxLength: 32, Metric: true},
	{Key: "ai_agent.broker.session.id", Scope: "trace", Destinations: []string{"local", "otlp", "broker"}, Cardinality: CardinalityHigh, MaxLength: 128},
	{Key: "ai_agent.verify.enabled", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, Metric: true},
	{Key: "ai_agent.attempt", Scope: "span", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, Metric: true},
	{Key: "ai_agent.attempt.outcome", Scope: "span", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, MaxLength: 32, Metric: true},
	{Key: "ai_agent.exit_code", Scope: "span", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, Metric: true},
	{Key: "ai_agent.command.sha256", Scope: "span", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh, MaxLength: 64},
	{Key: "ai_agent.usage.status", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, MaxLength: 32, Metric: true},
	{Key: "ai_agent.usage.source", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, MaxLength: 32, Metric: true},
	{Key: "ai_agent.usage.scope", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, MaxLength: 16, Metric: true},
	{Key: "ai_agent.usage.precision", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, MaxLength: 16, Metric: true},
	{Key: "ai_agent.usage.confidence", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, MaxLength: 32, Metric: true},
	{Key: "gen_ai.usage.input_tokens", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "gen_ai.usage.output_tokens", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "gen_ai.usage.cache_read.input_tokens", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "ai_agent.usage.cache_write.input_tokens", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "gen_ai.usage.reasoning.output_tokens", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "gen_ai.usage.total_tokens", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityHigh},
	{Key: "ai_agent.usage.cost.amount", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityHigh},
	{Key: "ai_agent.usage.cost.currency", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 8},
	{Key: "ai_agent.diagnostics.error_summary", Scope: "local", Destinations: []string{"local"}, Cardinality: CardinalityUnbounded, MaxLength: 512, Sensitive: true},
	{Key: "ai_agent.diagnostics.output_path", Scope: "local", Destinations: []string{"local"}, Cardinality: CardinalityUnbounded, MaxLength: 4096, Sensitive: true},
	{Key: "gen_ai.system", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 64, NativeInput: true},
	{Key: "gen_ai.response.id", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, MaxLength: MaxPropagatedValueLength, NativeInput: true},
	{Key: "service.name", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityWorkspace, MaxLength: 128, NativeInput: true},
	{Key: "service.namespace", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityWorkspace, MaxLength: 128, NativeInput: true},
	{Key: "service.version", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityWorkspace, MaxLength: 64, NativeInput: true},
	{Key: "telemetry.sdk.language", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 32, NativeInput: true},
	{Key: "telemetry.sdk.name", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 64, NativeInput: true},
	{Key: "telemetry.sdk.version", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 64, NativeInput: true},
	{Key: "span.type", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 64, NativeInput: true},
	{Key: "query_source", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 64, NativeInput: true},
	{Key: "duration_ms", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "ttft_ms", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "attempt", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, NativeInput: true},
	{Key: "success", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, NativeInput: true},
	{Key: "status_code", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, NativeInput: true},
	{Key: "stop_reason", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 64, NativeInput: true},
	{Key: "response.has_tool_call", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, NativeInput: true},
	{Key: "tool_name", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, MaxLength: 128, NativeInput: true},
	{Key: "result_tokens", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "decision", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 64, NativeInput: true},
	{Key: "source", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 64, NativeInput: true},
	{Key: "interaction.sequence", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "interaction.duration_ms", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "event.name", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 128, NativeInput: true},
	{Key: "event.kind", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 128, NativeInput: true},
	{Key: "cost_usd", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "user_prompt_length", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "prompt_length", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "tool_input_size_bytes", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "tool_result_size_bytes", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityHigh, NativeInput: true},
	{Key: "error_type", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 128, NativeInput: true},
	{Key: "speed", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 32, NativeInput: true},
	{Key: "effort", Scope: "native", Destinations: []string{"otlp"}, Cardinality: CardinalityLow, MaxLength: 32, NativeInput: true},
}

func validateFieldPolicies() error {
	seen := make(map[FieldID]struct{}, len(fieldRegistry))
	for _, field := range fieldRegistry {
		if field.Key == "" || field.Scope == "" || len(field.Destinations) == 0 {
			return fmt.Errorf("field policy is incomplete: %#v", field)
		}
		if _, exists := seen[field.Key]; exists {
			return fmt.Errorf("duplicate field policy %q", field.Key)
		}
		seen[field.Key] = struct{}{}
		if field.Metric && field.Cardinality != CardinalityLow {
			return fmt.Errorf("metric field %q must have low cardinality", field.Key)
		}
		if field.Sensitive && field.NativeInput {
			return fmt.Errorf("sensitive field %q cannot accept native input", field.Key)
		}
		if field.NativeInput && !slicesContains(field.Destinations, "otlp") {
			return fmt.Errorf("native field %q must allow OTLP", field.Key)
		}
	}
	return validateTraceProjection()
}

func validateTraceProjection() error {
	projected := make(map[string]int, len(spanAttributeProjections))
	for _, projection := range spanAttributeProjections {
		policy, ok := fieldPolicy(projection.key)
		if !ok {
			return fmt.Errorf("span projection %q has no field policy", projection.key)
		}
		if !isSpanScope(policy.Scope) {
			return fmt.Errorf("span projection %q must be a span-scoped field, got scope %q", projection.key, policy.Scope)
		}
		if !exportAllowed(projection.key) {
			return fmt.Errorf("span projection %q is sensitive or not OTLP-allowed", projection.key)
		}
		if projection.spans == 0 || projection.extract == nil {
			return fmt.Errorf("span projection %q needs a span placement and extractor", projection.key)
		}
		projected[projection.key]++
	}
	for _, field := range fieldRegistry {
		if !isSpanScope(field.Scope) || !slicesContains(field.Destinations, destOTLP) {
			continue
		}
		if field.Sensitive {
			return fmt.Errorf("span field %q is OTLP-allowed and must not be sensitive", field.Key)
		}
		count := projected[string(field.Key)]
		if count == 0 {
			return fmt.Errorf("span field %q is OTLP-capable but has no span projection", field.Key)
		}
		if count > 1 {
			return fmt.Errorf("span field %q has %d span projections; expected one", field.Key, count)
		}
	}
	return nil
}

func isSpanScope(scope string) bool {
	return scope == "trace" || scope == "span"
}

func fieldPolicy(key string) (FieldPolicy, bool) {
	for _, field := range fieldRegistry {
		if string(field.Key) == key {
			return field, true
		}
	}
	return FieldPolicy{}, false
}

func bounded(value string, maxLength int) string {
	value = strings.TrimSpace(value)
	if maxLength <= 0 || len(value) <= maxLength {
		return value
	}
	return value[:maxLength]
}

func boundedField(key, value string) string {
	policy, ok := fieldPolicy(key)
	if !ok {
		return value
	}
	return bounded(value, policy.MaxLength)
}

func fieldAllowed(key FieldID, destination string) bool {
	policy, ok := fieldPolicy(string(key))
	return ok && slicesContains(policy.Destinations, destination)
}

func SchemaReferenceMarkdown() string {
	var builder strings.Builder
	builder.WriteString("# Managed-Run Telemetry Schema\n\n")
	builder.WriteString("This document is generated from `internal/platform/telemetry/schema.go`. Run ")
	builder.WriteString("`go run ./cmd/telemetry-schema` after changing the field registry.\n\n")
	builder.WriteString("## Budgets\n\n")
	_, _ = fmt.Fprintf(&builder, "- Schema version: `%s`\n", SchemaVersion)
	_, _ = fmt.Fprintf(&builder, "- Root span attributes: at most %d\n", MaxRootAttributes)
	_, _ = fmt.Fprintf(&builder, "- Child span attributes: at most %d\n", MaxChildAttributes)
	_, _ = fmt.Fprintf(&builder, "- Span-event attributes: at most %d\n", MaxEventAttributes)
	_, _ = fmt.Fprintf(&builder, "- Propagated metadata and session values: at most %d characters\n", MaxPropagatedValueLength)
	_, _ = fmt.Fprintf(&builder, "- Tags: at most %d values of at most %d characters\n", MaxTagCount, MaxTagLength)
	_, _ = fmt.Fprintf(&builder, "- OTLP export payload: at most %d bytes\n", MaxOTLPExportPayloadBytes)
	_, _ = fmt.Fprintf(&builder, "- OTLP export structure: at most %d resource spans, %d scope spans, %d spans, and %d events per span\n\n", MaxExportResourceSpans, MaxExportScopeSpans, MaxExportSpans, MaxExportSpanEvents)
	builder.WriteString("High-cardinality values are retained on traces but are never metric dimensions. ")
	builder.WriteString("Sensitive and unbounded values remain local-only.\n\n")
	builder.WriteString("## Field Registry\n\n")
	builder.WriteString("| Field | Scope | Destinations | Cardinality | Max length | Sensitive | Metric |\n")
	builder.WriteString("|---|---|---|---|---:|---|---|\n")
	for _, field := range fieldRegistry {
		maxLength := "-"
		if field.MaxLength > 0 {
			maxLength = strconv.Itoa(field.MaxLength)
		}
		_, _ = fmt.Fprintf(&builder, "| `%s` | %s | %s | %s | %s | %t | %t |\n",
			field.Key, field.Scope, strings.Join(field.Destinations, ", "), field.Cardinality,
			maxLength, field.Sensitive, field.Metric)
	}
	builder.WriteString("\n## Versioning and Compatibility\n\n")
	builder.WriteString("History readers accept only events matching the current schema version and ")
	builder.WriteString("skip anything else, including crash-truncated lines. While the tool is pre-release ")
	builder.WriteString("with no durable consumers, an incompatible version bump is a deliberate clean break: ")
	builder.WriteString("older local records drop out of `ai-agent runs` rather than being migrated. Once a ")
	builder.WriteString("dashboard or meta-agent depends on this substrate, changes become additive within a ")
	builder.WriteString("major version and any breaking bump must ship a migration. See ADR 0003.\n\n")
	builder.WriteString("## Identity Semantics\n\n")
	builder.WriteString("- `run_id` identifies one managed invocation and maps to one trace.\n")
	builder.WriteString("- `broker.session_id` identifies the credential lease for that run.\n")
	builder.WriteString("- `task.ref` optionally groups multiple runs and maps to the Langfuse session ID.\n")
	builder.WriteString("- Requested and observed models remain separate; model family is the bounded aggregation dimension.\n")
	builder.WriteString("- Repository paths, diagnostic output, prompts, credentials, and complete commands are not exported.\n")
	return builder.String()
}
