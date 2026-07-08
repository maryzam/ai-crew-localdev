package homestate

import "strings"

var isolatedXDGVars = []string{
	"XDG_CONFIG_HOME",
	"XDG_DATA_HOME",
	"XDG_STATE_HOME",
	"XDG_CACHE_HOME",
}

func ApplyEnv(env []string, homeDir string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if key == "HOME" || isIsolatedXDGVar(key) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "HOME="+homeDir)
}

func EnvValue(env []string, name string) string {
	prefix := name + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return entry[len(prefix):]
		}
	}
	return ""
}

func isIsolatedXDGVar(key string) bool {
	for _, name := range isolatedXDGVars {
		if key == name {
			return true
		}
	}
	return false
}
