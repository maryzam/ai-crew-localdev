package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	MaxOTLPExportPayloadBytes = 1 << 20
	maxExportResourceSpans    = 8
	maxExportScopeSpans       = 8
	maxExportSpans            = 128
	maxExportEvents           = 32
)

func ValidateOTLPExportPayload(data []byte) error {
	if len(data) == 0 || len(data) > MaxOTLPExportPayloadBytes {
		return fmt.Errorf("OTLP payload size %d exceeds allowed range 1..%d", len(data), MaxOTLPExportPayloadBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload otlpPayload
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("decode OTLP payload: %w", err)
	}
	if err := consumeEOF(decoder); err != nil {
		return err
	}
	if len(payload.ResourceSpans) == 0 || len(payload.ResourceSpans) > maxExportResourceSpans {
		return fmt.Errorf("OTLP resource span count %d exceeds allowed range 1..%d", len(payload.ResourceSpans), maxExportResourceSpans)
	}
	spanCount := 0
	for _, resource := range payload.ResourceSpans {
		if err := validateExportAttributes(resource.Resource.Attributes, MaxRootAttributes); err != nil {
			return fmt.Errorf("OTLP resource attributes: %w", err)
		}
		if len(resource.ScopeSpans) == 0 || len(resource.ScopeSpans) > maxExportScopeSpans {
			return fmt.Errorf("OTLP scope span count %d exceeds allowed range 1..%d", len(resource.ScopeSpans), maxExportScopeSpans)
		}
		for _, scope := range resource.ScopeSpans {
			if !allowedExportScope(scope.Scope.Name) || len(scope.Scope.Version) > MaxPropagatedValueLength {
				return fmt.Errorf("OTLP instrumentation scope is not allowed")
			}
			for _, span := range scope.Spans {
				spanCount++
				if spanCount > maxExportSpans {
					return fmt.Errorf("OTLP span count exceeds %d", maxExportSpans)
				}
				if err := validateExportSpan(span); err != nil {
					return err
				}
			}
		}
	}
	if spanCount == 0 {
		return fmt.Errorf("OTLP payload contains no spans")
	}
	return nil
}

func consumeEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("OTLP payload contains multiple JSON values")
		}
		return fmt.Errorf("decode OTLP payload trailer: %w", err)
	}
	return nil
}

func allowedExportScope(name string) bool {
	return name == "ai-agent-native" || name == "github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
}

func validateExportSpan(span otlpSpan) error {
	if !allowedExportSpanName(span.Name) {
		return fmt.Errorf("OTLP span name is not allowed")
	}
	if safeHex(span.TraceID, 32) == "" || safeHex(span.SpanID, 16) == "" {
		return fmt.Errorf("OTLP span identity is invalid")
	}
	if span.ParentSpanID != "" && safeHex(span.ParentSpanID, 16) == "" {
		return fmt.Errorf("OTLP parent span identity is invalid")
	}
	if safeDecimal(span.StartTimeUnixNano) == "" || safeDecimal(span.EndTimeUnixNano) == "" {
		return fmt.Errorf("OTLP span timestamps are invalid")
	}
	if span.Kind < 0 || span.Kind > 5 || span.Status.Code < 0 || span.Status.Code > 2 || span.Status.Message != "" {
		return fmt.Errorf("OTLP span status is invalid")
	}
	if err := validateExportAttributes(span.Attributes, MaxRootAttributes); err != nil {
		return fmt.Errorf("OTLP span attributes: %w", err)
	}
	if len(span.Events) > maxExportEvents {
		return fmt.Errorf("OTLP span event count exceeds %d", maxExportEvents)
	}
	for _, event := range span.Events {
		if !allowedExportEventName(event.Name) || safeDecimal(event.TimeUnixNano) == "" {
			return fmt.Errorf("OTLP span event is invalid")
		}
		if err := validateExportAttributes(event.Attributes, MaxEventAttributes); err != nil {
			return fmt.Errorf("OTLP span event attributes: %w", err)
		}
	}
	return nil
}

func allowedExportSpanName(name string) bool {
	if name == "ai_agent.run" || name == "agent.command" || name == "verify.attempt" || name == "agent.operation" {
		return true
	}
	return len(name) <= 128 && (strings.HasPrefix(name, "claude_code.") || strings.HasPrefix(name, "codex.") || strings.HasPrefix(name, "gen_ai."))
}

func allowedExportEventName(name string) bool {
	switch name {
	case "session.created", "session.revoked", "model.resolved", "usage.recorded", "agent.event":
		return true
	default:
		return len(name) <= 128 && (strings.HasPrefix(name, "claude_code.") || strings.HasPrefix(name, "codex.") || strings.HasPrefix(name, "gen_ai."))
	}
}

func validateExportAttributes(attributes []otlpWireAttribute, limit int) error {
	if len(attributes) > limit {
		return fmt.Errorf("attribute count %d exceeds %d", len(attributes), limit)
	}
	seen := make(map[string]struct{}, len(attributes))
	for _, attribute := range attributes {
		if _, exists := seen[attribute.Key]; exists {
			return fmt.Errorf("duplicate attribute %q", attribute.Key)
		}
		seen[attribute.Key] = struct{}{}
		maxLength, ok := exportAttributeLimit(attribute.Key)
		if !ok {
			return fmt.Errorf("attribute %q is not allowed", attribute.Key)
		}
		if err := validateExportValue(attribute.Value, maxLength); err != nil {
			return fmt.Errorf("attribute %q: %w", attribute.Key, err)
		}
	}
	return nil
}

func exportAttributeLimit(key string) (int, bool) {
	if policy, ok := fieldPolicy(key); ok && !policy.Sensitive && slicesContains(policy.Destinations, destOTLP) {
		if policy.MaxLength > 0 {
			return policy.MaxLength, true
		}
		return MaxPropagatedValueLength, true
	}
	for _, policy := range resourceAttributesPolicy {
		if policy.key == key {
			return MaxPropagatedValueLength, true
		}
	}
	for _, policy := range langfuseAttributesPolicy {
		if policy.key == key {
			return MaxPropagatedValueLength, true
		}
	}
	return 0, false
}

func validateExportValue(value otlpWireValue, maxLength int) error {
	variants := 0
	if value.ArrayValue != nil {
		variants++
		if len(value.ArrayValue.Values) > MaxTagCount {
			return fmt.Errorf("array length exceeds %d", MaxTagCount)
		}
		for _, item := range value.ArrayValue.Values {
			if item.StringValue == nil || len(*item.StringValue) > MaxTagLength {
				return fmt.Errorf("array value is not a bounded string")
			}
		}
	}
	if value.BoolValue != nil {
		variants++
	}
	if value.DoubleValue != nil {
		variants++
	}
	if value.IntValue != nil {
		variants++
		if !decimal(*value.IntValue) {
			return fmt.Errorf("integer value is invalid")
		}
	}
	if value.StringValue != nil {
		variants++
		if len(*value.StringValue) > maxLength {
			return fmt.Errorf("string length exceeds %d", maxLength)
		}
	}
	if variants != 1 {
		return fmt.Errorf("value must contain exactly one supported type")
	}
	return nil
}
