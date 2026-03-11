package broker

import (
	"testing"
)

func TestSerializePermissions(t *testing.T) {
	tests := []struct {
		name  string
		perms map[string]string
		want  string
	}{
		{"nil", nil, ""},
		{"empty", map[string]string{}, ""},
		{"single", map[string]string{"contents": "write"}, "contents=write"},
		{"sorted", map[string]string{
			"pull_requests": "write",
			"contents":      "write",
			"metadata":      "read",
		}, "contents=write,metadata=read,pull_requests=write"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SerializePermissions(tt.perms)
			if got != tt.want {
				t.Errorf("SerializePermissions = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSerializePermissionsDeterministic(t *testing.T) {
	perms := map[string]string{
		"contents":      "write",
		"pull_requests": "write",
		"metadata":      "read",
		"issues":        "write",
	}

	first := SerializePermissions(perms)
	for i := 0; i < 100; i++ {
		if got := SerializePermissions(perms); got != first {
			t.Fatalf("iteration %d: got %q, want %q", i, got, first)
		}
	}
}

func TestValidatePermissionSubset(t *testing.T) {
	allowed := map[string]string{
		"contents":      "write",
		"pull_requests": "write",
		"metadata":      "read",
	}

	tests := []struct {
		name      string
		requested map[string]string
		wantErr   bool
	}{
		{"empty is subset", map[string]string{}, false},
		{"exact match", allowed, false},
		{"subset", map[string]string{"metadata": "read"}, false},
		{"escalation", map[string]string{"metadata": "write"}, true},
		{"unknown key", map[string]string{"deployments": "read"}, true},
		{"read within write", map[string]string{"contents": "read"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePermissionSubset(tt.requested, allowed)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePermissionSubset error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMergePermissions(t *testing.T) {
	session := map[string]string{"contents": "write", "metadata": "read"}

	t.Run("nil requested uses session", func(t *testing.T) {
		got, err := MergePermissions(session, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["contents"] != "write" {
			t.Error("expected session permissions")
		}
	})

	t.Run("valid subset", func(t *testing.T) {
		got, err := MergePermissions(session, map[string]string{"metadata": "read"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := got["contents"]; ok {
			t.Error("expected only requested permissions")
		}
	})

	t.Run("escalation rejected", func(t *testing.T) {
		_, err := MergePermissions(session, map[string]string{"metadata": "admin"})
		if err == nil {
			t.Error("expected error for escalation")
		}
	})
}
