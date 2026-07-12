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

func TestPolicySectionWireShape(t *testing.T) {
	data, err := json.Marshal(PolicySection{InstallationID: 42, DefaultPermissions: map[string]string{"contents": "write"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"installation_id":42,"default_permissions":{"contents":"write"}}` {
		t.Fatalf("policy section wire shape = %s", data)
	}
}
