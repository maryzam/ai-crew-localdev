package contract

import (
	"encoding/json"
	"testing"
)

func TestCredentialWireShape(t *testing.T) {
	data, err := json.Marshal(Credential{Token: "ghs_xxx"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"token":"ghs_xxx"}` {
		t.Fatalf("credential wire shape = %s", data)
	}
}
