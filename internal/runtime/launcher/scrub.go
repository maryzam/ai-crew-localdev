package launcher

import (
	"github.com/maryzam/ai-crew-localdev/internal/control/plan"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"strconv"
	"strings"
)

var sessionEnvVars = []string{
	paths.EnvAuthSock,
	paths.EnvSessionID,
	paths.EnvSessionBindFD,
	paths.EnvSessionRepo,
	paths.EnvRunID,
	paths.EnvTaskRef,
	paths.EnvRealGh,
	paths.EnvContainer,
}

func ScrubEnv(env []string, profiles []plan.InterceptionProfile, credentialHelperPath string, socketPath string, sessionID string, bindFD int, sessionRepo string, ghWrapperDir string, realGhPath string) []string {
	result := applyPlannedScrub(env, profiles)

	result = append(result, paths.EnvAuthSock+"="+socketPath)
	result = append(result, paths.EnvSessionID+"="+sessionID)
	result = append(result, paths.EnvSessionBindFD+"="+strconv.Itoa(bindFD))
	result = append(result, paths.EnvSessionRepo+"="+sessionRepo)
	if realGhPath != "" {
		result = append(result, paths.EnvRealGh+"="+realGhPath)
	}

	if ghWrapperDir != "" {
		result = prependPath(result, ghWrapperDir)
	}

	return result
}

func applyPlannedScrub(env []string, profiles []plan.InterceptionProfile) []string {
	names := map[string]struct{}{}
	for _, name := range sessionEnvVars {
		names[name] = struct{}{}
	}
	var prefixes []string
	for _, profile := range profiles {
		for _, name := range profile.ScrubEnv {
			names[name] = struct{}{}
		}
		prefixes = append(prefixes, profile.ScrubEnvPrefixes...)
	}

	result := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if _, scrubbed := names[key]; scrubbed {
			continue
		}
		if matchesAnyPrefix(key, prefixes) {
			continue
		}
		result = append(result, entry)
	}
	for _, profile := range profiles {
		for _, variable := range profile.FailClosedEnv {
			result = append(result, variable.Name+"="+variable.Value)
		}
	}
	return result
}

func matchesAnyPrefix(key string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func prependPath(env []string, dir string) []string {
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + dir + ":" + e[5:]
			return env
		}
	}
	return append(env, "PATH="+dir)
}
