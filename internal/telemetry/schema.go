package telemetry

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/correlation"
)

const (
	SchemaVersion            = "1.0"
	MaxRootAttributes        = 48
	MaxChildAttributes       = 24
	MaxEventAttributes       = 12
	MaxPropagatedMetadata    = 8
	MaxPropagatedValueLength = 200
	MaxSessionIDLength       = correlation.MaxTaskRefLength
	MaxTagCount              = 8
	MaxTagLength             = 64
)

type Cardinality string

const (
	CardinalityLow       Cardinality = "low"
	CardinalityWorkspace Cardinality = "workspace"
	CardinalityHigh      Cardinality = "high"
	CardinalityUnbounded Cardinality = "unbounded"
)

type FieldPolicy struct {
	Key          string
	Scope        string
	Destinations []string
	Cardinality  Cardinality
	MaxLength    int
	Sensitive    bool
	Metric       bool
}

var FieldPolicies = []FieldPolicy{
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
	{Key: "gen_ai.request.model", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityHigh, MaxLength: MaxPropagatedValueLength},
	{Key: "gen_ai.response.model", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityHigh, MaxLength: MaxPropagatedValueLength},
	{Key: "ai_agent.model.family", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 64, Metric: true},
	{Key: "ai_agent.model.confidence", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 16, Metric: true},
	{Key: "ai_agent.model.source", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, MaxLength: 32, Metric: true},
	{Key: "ai_agent.broker.session.id", Scope: "trace", Destinations: []string{"local", "otlp", "broker"}, Cardinality: CardinalityHigh, MaxLength: 128},
	{Key: "ai_agent.verify.enabled", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, Metric: true},
	{Key: "ai_agent.attempt", Scope: "span", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, Metric: true},
	{Key: "ai_agent.exit_code", Scope: "span", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, Metric: true},
	{Key: "ai_agent.command.sha256", Scope: "span", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh, MaxLength: 64},
	{Key: "ai_agent.usage.status", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityLow, MaxLength: 32, Metric: true},
	{Key: "gen_ai.usage.input_tokens", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh},
	{Key: "gen_ai.usage.output_tokens", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh},
	{Key: "gen_ai.usage.cache_read.input_tokens", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh},
	{Key: "ai_agent.usage.cache_write.input_tokens", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh},
	{Key: "gen_ai.usage.reasoning.output_tokens", Scope: "trace", Destinations: []string{"local", "otlp"}, Cardinality: CardinalityHigh},
	{Key: "gen_ai.usage.total_tokens", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityHigh},
	{Key: "ai_agent.usage.cost.amount", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityHigh},
	{Key: "ai_agent.usage.cost.currency", Scope: "trace", Destinations: []string{"local", "otlp", "langfuse"}, Cardinality: CardinalityLow, MaxLength: 8},
	{Key: "ai_agent.diagnostics.error_summary", Scope: "local", Destinations: []string{"local"}, Cardinality: CardinalityUnbounded, MaxLength: 512, Sensitive: true},
	{Key: "ai_agent.diagnostics.output_path", Scope: "local", Destinations: []string{"local"}, Cardinality: CardinalityUnbounded, MaxLength: 4096, Sensitive: true},
}

var metricDimensions = map[string]struct{}{
	"ai_agent.schema.version":     {},
	"ai_agent.run.mode":           {},
	"ai_agent.run.outcome":        {},
	"ai_agent.run.terminal_phase": {},
	"ai_agent.repository.dirty":   {},
	"ai_agent.agent.type":         {},
	"gen_ai.provider.name":        {},
	"ai_agent.model.family":       {},
	"ai_agent.model.confidence":   {},
	"ai_agent.model.source":       {},
	"ai_agent.verify.enabled":     {},
	"ai_agent.attempt":            {},
	"ai_agent.exit_code":          {},
	"ai_agent.usage.status":       {},
}

func ValidateFieldPolicies() error {
	seen := make(map[string]struct{}, len(FieldPolicies))
	for _, field := range FieldPolicies {
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
	}
	for key := range metricDimensions {
		field, ok := fieldPolicy(key)
		if !ok || !field.Metric {
			return fmt.Errorf("metric dimension %q lacks a metric field policy", key)
		}
	}
	return nil
}

func fieldPolicy(key string) (FieldPolicy, bool) {
	for _, field := range FieldPolicies {
		if field.Key == key {
			return field, true
		}
	}
	return FieldPolicy{}, false
}

func MetricDimensions() []string {
	result := make([]string, 0, len(metricDimensions))
	for _, field := range FieldPolicies {
		if field.Metric {
			result = append(result, field.Key)
		}
	}
	return result
}

func ValidateTaskRef(value string) error {
	return correlation.ValidateTaskRef(value)
}

func validMetadataKey(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
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

func SchemaReferenceMarkdown() string {
	var builder strings.Builder
	builder.WriteString("# Managed-Run Telemetry Schema\n\n")
	builder.WriteString("This document is generated from `internal/telemetry/schema.go`. Run ")
	builder.WriteString("`go run ./cmd/telemetry-schema` after changing the field registry.\n\n")
	builder.WriteString("## Budgets\n\n")
	_, _ = fmt.Fprintf(&builder, "- Schema version: `%s`\n", SchemaVersion)
	_, _ = fmt.Fprintf(&builder, "- Root span attributes: at most %d\n", MaxRootAttributes)
	_, _ = fmt.Fprintf(&builder, "- Child span attributes: at most %d\n", MaxChildAttributes)
	_, _ = fmt.Fprintf(&builder, "- Span-event attributes: at most %d\n", MaxEventAttributes)
	_, _ = fmt.Fprintf(&builder, "- Propagated Langfuse metadata fields: at most %d\n", MaxPropagatedMetadata)
	_, _ = fmt.Fprintf(&builder, "- Propagated metadata and session values: at most %d characters\n", MaxPropagatedValueLength)
	_, _ = fmt.Fprintf(&builder, "- Tags: at most %d values of at most %d characters\n\n", MaxTagCount, MaxTagLength)
	builder.WriteString("High-cardinality values are retained on traces but are never metric dimensions. ")
	builder.WriteString("Sensitive and unbounded values remain local-only.\n\n")
	builder.WriteString("## Field Registry\n\n")
	builder.WriteString("| Field | Scope | Destinations | Cardinality | Max length | Sensitive | Metric |\n")
	builder.WriteString("|---|---|---|---|---:|---|---|\n")
	for _, field := range FieldPolicies {
		maxLength := "-"
		if field.MaxLength > 0 {
			maxLength = strconv.Itoa(field.MaxLength)
		}
		_, _ = fmt.Fprintf(&builder, "| `%s` | %s | %s | %s | %s | %t | %t |\n",
			field.Key, field.Scope, strings.Join(field.Destinations, ", "), field.Cardinality,
			maxLength, field.Sensitive, field.Metric)
	}
	builder.WriteString("\n## Identity Semantics\n\n")
	builder.WriteString("- `run_id` identifies one managed invocation and maps to one trace.\n")
	builder.WriteString("- `broker.session_id` identifies the credential lease for that run.\n")
	builder.WriteString("- `task.ref` optionally groups multiple runs and maps to the Langfuse session ID.\n")
	builder.WriteString("- Requested and observed models remain separate; model family is the bounded aggregation dimension.\n")
	builder.WriteString("- Repository paths, diagnostic output, prompts, credentials, and complete commands are not exported.\n")
	return builder.String()
}
