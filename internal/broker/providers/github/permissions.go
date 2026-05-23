package github

import (
	"fmt"
	"sort"
	"strings"
)

type permissionLevel int

const (
	levelUnknown permissionLevel = iota
	levelRead
	levelWrite
	levelAdmin
)

func levelOf(s string) permissionLevel {
	switch s {
	case "read":
		return levelRead
	case "write":
		return levelWrite
	case "admin":
		return levelAdmin
	default:
		return levelUnknown
	}
}

func validatePermissionSubset(requested, allowed map[string]string) error {
	for key, want := range requested {
		wantLevel := levelOf(want)
		if wantLevel == levelUnknown {
			return fmt.Errorf("permission %q: invalid level %q (allowed: read, write, admin)", key, want)
		}
		grantedRaw, ok := allowed[key]
		if !ok {
			return fmt.Errorf("permission %q: not granted by policy", key)
		}
		grantedLevel := levelOf(grantedRaw)
		if grantedLevel == levelUnknown {
			return fmt.Errorf("permission %q: policy default %q is invalid", key, grantedRaw)
		}
		if wantLevel > grantedLevel {
			return fmt.Errorf("permission %q: requested %q exceeds policy default %q", key, want, grantedRaw)
		}
	}
	return nil
}

func serializePermissions(perms map[string]string) string {
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
