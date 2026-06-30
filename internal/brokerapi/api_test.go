package brokerapi

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCreateSessionResponseIdleTimeoutWireFormat(t *testing.T) {
	response := CreateSessionResponse{SessionID: "sess-wire", IdleTimeout: DurationString(90 * time.Minute)}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	var idleTimeout string
	if err := json.Unmarshal(wire["idle_timeout"], &idleTimeout); err != nil {
		t.Fatalf("idle_timeout is not a JSON string: %s: %v", wire["idle_timeout"], err)
	}
	if idleTimeout != "1h30m0s" {
		t.Fatalf("idle_timeout = %q", idleTimeout)
	}
}
