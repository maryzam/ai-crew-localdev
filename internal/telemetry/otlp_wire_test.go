package telemetry

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
)

func TestOTLPPayloadPreservesLegacyJSONBytes(t *testing.T) {
	event := representativeEvent()
	event.EventType = "run.finished"
	payload, err := buildOTLPPayload([]Event{event})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if digest := fmt.Sprintf("%x", sha256.Sum256(data)); digest != "bfbb47331fba4c7d6a4ef8025f5b2bd969ed2e0e75d509770e36387a70426e56" {
		t.Fatalf("OTLP JSON digest = %s\npayload = %s", digest, data)
	}
}

func TestOTLPWireDTOsPreserveOptionalFieldBytes(t *testing.T) {
	emptyEvents := []otlpSpanEvent{}
	payload := otlpPayload{ResourceSpans: []otlpResourceSpans{{Resource: otlpResource{Attributes: []otlpWireAttribute{}}, ScopeSpans: []otlpScopeSpans{{Scope: otlpScope{Name: "scope", Version: "1"}, Spans: []otlpSpan{{Attributes: []otlpWireAttribute{}, EndTimeUnixNano: "2", Events: &emptyEvents, Kind: 1, Name: "root", SpanID: "01", StartTimeUnixNano: "1", Status: otlpStatus{}, TraceID: "02"}, {Attributes: []otlpWireAttribute{}, EndTimeUnixNano: "3", Kind: 1, Name: "child", ParentSpanID: "01", SpanID: "03", StartTimeUnixNano: "2", Status: otlpStatus{Code: 1}, TraceID: "02"}}}}}}}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"resourceSpans":[{"resource":{"attributes":[]},"scopeSpans":[{"scope":{"name":"scope","version":"1"},"spans":[{"attributes":[],"endTimeUnixNano":"2","events":[],"kind":1,"name":"root","spanId":"01","startTimeUnixNano":"1","status":{},"traceId":"02"},{"attributes":[],"endTimeUnixNano":"3","kind":1,"name":"child","parentSpanId":"01","spanId":"03","startTimeUnixNano":"2","status":{"code":1},"traceId":"02"}]}]}]}`
	if string(data) != want {
		t.Fatalf("OTLP JSON = %s\nwant = %s", data, want)
	}
}
