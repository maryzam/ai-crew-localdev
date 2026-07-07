package contract

import "github.com/maryzam/ai-crew-localdev/internal/interception"

func InterceptionProfile() interception.Profile {
	return interception.Profile{
		Provider: "github",
		Commands: []string{"gh"},
		ScrubEnv: []string{
			"GH_TOKEN",
			"GITHUB_TOKEN",
			"GH_ENTERPRISE_TOKEN",
			"GITHUB_ENTERPRISE_TOKEN",
			"GH_HOST",
			"SSH_AUTH_SOCK",
			"GIT_SSH",
			"GIT_SSH_COMMAND",
			"SSH_ASKPASS",
			"GIT_ASKPASS",
			"GIT_CONFIG_GLOBAL",
			"GIT_CONFIG_SYSTEM",
			"GIT_CONFIG_COUNT",
		},
		ScrubEnvPrefixes: []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"},
		FailClosedEnv:    failClosedGitEnv,
	}
}

func failClosedGitEnv(session interception.Session) []string {
	repoURL := "https://github.com/" + session.Repo
	return []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=7",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=credential.helper",
		"GIT_CONFIG_VALUE_1=" + session.CredentialHelperPath,
		"GIT_CONFIG_KEY_2=credential.https://github.com.useHttpPath",
		"GIT_CONFIG_VALUE_2=true",
		"GIT_CONFIG_KEY_3=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_3=",
		"GIT_CONFIG_KEY_4=http." + repoURL + ".extraheader",
		"GIT_CONFIG_VALUE_4=",
		"GIT_CONFIG_KEY_5=http." + repoURL + ".git.extraheader",
		"GIT_CONFIG_VALUE_5=",
		"GIT_CONFIG_KEY_6=http.extraheader",
		"GIT_CONFIG_VALUE_6=",
	}
}
