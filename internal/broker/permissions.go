package broker

import (
	"fmt"
	"sort"
	"strings"
)

// SerializePermissions produces a deterministic string from a permission map
// for use as a cache key component. Keys are sorted alphabetically and joined
// as "key1=val1,key2=val2".
func SerializePermissions(perms map[string]string) string {
	if len(perms) == 0 {
		return ""
	}
	keys := make([]string, 0, len(perms))
	for k := range perms {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + perms[k]
	}
	return strings.Join(parts, ",")
}

// ValidatePermissionSubset checks that every permission in requested is
// present in allowed and does not exceed the allowed level.
// The permission hierarchy is: read < write < admin.
func ValidatePermissionSubset(requested, allowed map[string]string) error {
	for key, reqLevel := range requested {
		allowedLevel, ok := allowed[key]
		if !ok {
			return fmt.Errorf("permission %q not in allowed set", key)
		}
		if permissionRank(reqLevel) > permissionRank(allowedLevel) {
			return fmt.Errorf("permission %q: requested %q exceeds allowed %q", key, reqLevel, allowedLevel)
		}
	}
	return nil
}

// MergePermissions returns the effective permission set for a token request.
// If requested is nil or empty, session permissions are used. Otherwise,
// requested must be a subset of session permissions.
func MergePermissions(session, requested map[string]string) (map[string]string, error) {
	if len(requested) == 0 {
		return session, nil
	}
	if err := ValidatePermissionSubset(requested, session); err != nil {
		return nil, err
	}
	return requested, nil
}

func permissionRank(level string) int {
	switch level {
	case "read":
		return 1
	case "write":
		return 2
	case "admin":
		return 3
	default:
		return 0
	}
}
