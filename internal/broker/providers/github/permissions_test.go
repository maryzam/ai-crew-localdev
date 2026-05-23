package github

import "testing"

func TestValidatePermissionSubset(t *testing.T) {
	allowed := map[string]string{
		"contents":      "write",
		"pull_requests": "write",
		"metadata":      "read",
	}

	cases := []struct {
		name      string
		requested map[string]string
		wantErr   string
	}{
		{name: "empty downscope", requested: nil},
		{name: "exact match", requested: map[string]string{"contents": "write"}},
		{name: "downscope write to read", requested: map[string]string{"contents": "read"}},
		{name: "subset of allowed keys", requested: map[string]string{"metadata": "read"}},

		{
			name:      "escalate read to write",
			requested: map[string]string{"metadata": "write"},
			wantErr:   `permission "metadata": requested "write" exceeds policy default "read"`,
		},
		{
			name:      "escalate write to admin",
			requested: map[string]string{"contents": "admin"},
			wantErr:   `permission "contents": requested "admin" exceeds policy default "write"`,
		},
		{
			name:      "key not in policy",
			requested: map[string]string{"workflows": "write"},
			wantErr:   `permission "workflows": not granted by policy`,
		},
		{
			name:      "invalid requested level",
			requested: map[string]string{"contents": "owner"},
			wantErr:   `permission "contents": invalid level "owner"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePermissionSubset(tc.requested, allowed)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %q, got nil", tc.wantErr)
			}
			if got := err.Error(); !contains(got, tc.wantErr) {
				t.Fatalf("error mismatch:\n  got:  %s\n  want: %s", got, tc.wantErr)
			}
		})
	}
}

func TestSerializePermissionsDeterministic(t *testing.T) {
	a := serializePermissions(map[string]string{"b": "write", "a": "read"})
	b := serializePermissions(map[string]string{"a": "read", "b": "write"})
	if a != b {
		t.Errorf("non-deterministic serialization: %q vs %q", a, b)
	}
	if a != "a=read,b=write" {
		t.Errorf("got %q, want a=read,b=write", a)
	}
	if serializePermissions(nil) != "" {
		t.Errorf("nil should serialize to empty string")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
