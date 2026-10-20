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
