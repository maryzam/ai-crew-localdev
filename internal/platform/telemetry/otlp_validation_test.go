package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGeneratedOTLPPayloadPassesBrokerEgressValidation(t *testing.T) {
	payload, err := buildOTLPPayload([]Event{representativeEvent(), representativeEventFinished()})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOTLPExportPayload(data); err != nil {
		t.Fatalf("generated payload rejected: %v", err)
	}
}

func nativeExportSpan() otlpSpan {
	return otlpSpan{
		TraceID:           "0123456789abcdef0123456789abcdef",
		SpanID:            "0123456789abcdef",
		Name:              "gen_ai.generate_content",
		Kind:              1,
		StartTimeUnixNano: "1",
		EndTimeUnixNano:   "2",
	}
}

func TestSanitizedNativeBatchSplitsWithinBrokerEgressBounds(t *testing.T) {
	spans := make([]otlpSpan, MaxExportSpans*4+7)
	for i := range spans {
		spans[i] = nativeExportSpan()
	}
	spans[MaxExportSpans].TraceID = "invalid"
	input := otlpPayload{ResourceSpans: []otlpResourceSpans{{
		ScopeSpans: []otlpScopeSpans{{Scope: otlpScope{Name: nativeScopeName}, Spans: spans}},
	}}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOTLPExportPayload(raw); err == nil || !strings.Contains(err.Error(), "span count") {
		t.Fatalf("oversized native batch validation error = %v", err)
	}

	payloads, dropped := sanitizeNativePayloads(input, RunSummary{})
	if dropped != 1 {
		t.Fatalf("malformed native spans dropped = %d, want 1", dropped)
	}
	if len(payloads) < 5 {
		t.Fatalf("oversized native batch was not split: got %d payloads", len(payloads))
	}
	exported := 0
	for _, payload := range payloads {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateOTLPExportPayload(data); err != nil {
			t.Fatalf("sanitized native payload rejected by egress gate: %v", err)
		}
		for _, resource := range payload.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				exported += len(scope.Spans)
			}
		}
	}
	if exported != len(spans)-1 {
		t.Fatalf("split exported %d spans, want %d valid spans", exported, len(spans)-1)
	}
}

func TestSanitizedNativeSpanCapsEventsToExportBudget(t *testing.T) {
	span := nativeExportSpan()
	span.Events = make([]otlpSpanEvent, MaxExportSpanEvents+10)
	for i := range span.Events {
		attributes := make([]otlpWireAttribute, 0, MaxEventAttributes+5)
		for _, field := range fieldRegistry {
			if field.NativeInput {
				attributes = append(attributes, newOTLPWireAttribute(string(field.Key), strings.Repeat("x", MaxPropagatedValueLength+10)))
				if len(attributes) == MaxEventAttributes+5 {
					break
				}
			}
		}
		span.Events[i] = otlpSpanEvent{Name: "gen_ai.choice", TimeUnixNano: "1", Attributes: attributes}
	}
	span.Events[0].TimeUnixNano = "invalid"
	input := otlpPayload{ResourceSpans: []otlpResourceSpans{{
		ScopeSpans: []otlpScopeSpans{{Scope: otlpScope{Name: "claude-code"}, Spans: []otlpSpan{span}}},
	}}}

	payloads, dropped := sanitizeNativePayloads(input, RunSummary{})
	if dropped != 0 {
		t.Fatalf("malformed native spans dropped = %d", dropped)
	}
	data, err := json.Marshal(payloads[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOTLPExportPayload(data); err != nil {
		t.Fatalf("event-capped native payload rejected by egress gate: %v", err)
	}
	events := payloads[0].ResourceSpans[0].ScopeSpans[0].Spans[0].Events
	if len(events) != MaxExportSpanEvents {
		t.Fatalf("sanitized events = %d, want %d valid events", len(events), MaxExportSpanEvents)
	}
	for _, event := range events {
		if len(event.Attributes) > MaxEventAttributes {
			t.Fatalf("event attributes = %d, budget %d", len(event.Attributes), MaxEventAttributes)
		}
	}
}

func TestSanitizedNativeAttributesAreCanonicalAndBounded(t *testing.T) {
	longValue := strings.Repeat("7", MaxPropagatedValueLength+50)
	attributes := []otlpWireAttribute{
		newOTLPWireAttribute("input_tokens", longValue),
		newOTLPWireAttribute("gen_ai.usage.input_tokens", longValue),
	}
	span := nativeExportSpan()
	span.Attributes = attributes
	input := otlpPayload{ResourceSpans: []otlpResourceSpans{{ScopeSpans: []otlpScopeSpans{{Spans: []otlpSpan{span}}}}}}

	payloads, dropped := sanitizeNativePayloads(input, RunSummary{})
	if dropped != 0 {
		t.Fatalf("malformed native spans dropped = %d", dropped)
	}
	data, err := json.Marshal(payloads[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOTLPExportPayload(data); err != nil {
		t.Fatalf("canonical native payload rejected by egress gate: %v", err)
	}
	got := payloads[0].ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes
	if len(got) != 1 || got[0].Value.StringValue == nil || len(*got[0].Value.StringValue) != MaxPropagatedValueLength {
		t.Fatalf("sanitized attributes = %#v", got)
	}
}

func TestFailedRunExportsBoundedOutcomeWithoutStatusMessage(t *testing.T) {
	event := representativeEventFinished()
	event.Outcome = OutcomeAgentFailed
	event.Run.Outcome = OutcomeAgentFailed
	payload, err := buildOTLPPayload([]Event{event})
	if err != nil {
		t.Fatal(err)
	}
	span := payload.ResourceSpans[0].ScopeSpans[0].Spans[0]
	if span.Status.Code != 2 || span.Status.Message != "" {
		t.Fatalf("span status = %#v", span.Status)
	}
	if got := attributeValue(span.Attributes, "ai_agent.run.outcome"); got != OutcomeAgentFailed {
		t.Fatalf("run outcome attribute = %q", got)
	}
}

func TestBrokerEgressValidationRejectsUnknownContentField(t *testing.T) {
	payload, err := buildOTLPPayload([]Event{representativeEvent(), representativeEventFinished()})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	withPrompt := strings.Replace(string(data), `"name":"ai_agent.run"`, `"name":"ai_agent.run","prompt":"private"`, 1)
	if err := ValidateOTLPExportPayload([]byte(withPrompt)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("validation error = %v", err)
	}
}
