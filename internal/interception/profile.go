package interception

import "strings"

type Session struct {
	Repo                 string
	CredentialHelperPath string
}

type Profile struct {
	Provider         string
	ScrubEnv         []string
	ScrubEnvPrefixes []string
	Commands         []string
	FailClosedEnv    func(Session) []string
}

func Apply(env []string, profiles []Profile, session Session) []string {
	names := make(map[string]struct{})
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
		if profile.FailClosedEnv != nil {
			result = append(result, profile.FailClosedEnv(session)...)
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
