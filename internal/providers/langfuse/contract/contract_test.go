package contract

import (
	"encoding/json"
	"testing"
)

func TestCredentialWireShape(t *testing.T) {
	data, err := json.Marshal(Credential{Endpoint: "http://localhost:3000/api/public/otel", PublicKey: "pk-test", SecretKey: "sk-test"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"endpoint":"http://localhost:3000/api/public/otel","public_key":"pk-test","secret_key":"sk-test"}` {
		t.Fatalf("credential wire shape = %s", data)
	}
}
