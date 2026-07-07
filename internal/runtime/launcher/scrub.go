package launcher

import (
	"strconv"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/interception"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
	langfusecontract "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse/contract"
)

var sessionEnvVars = []string{
	"AI_AGENT_AUTH_SOCK",
	"AI_AGENT_SESSION_ID",
	"AI_AGENT_SESSION_BIND_FD",
	"AI_AGENT_SESSION_REPO",
	"AI_AGENT_RUN_ID",
	"AI_AGENT_TASK_REF",
	"AI_AGENT_REAL_GH",
	"AI_AGENT_CONTAINER",
}

func interceptionProfiles() []interception.Profile {
	return []interception.Profile{
		{Provider: "session", ScrubEnv: sessionEnvVars},
		githubcontract.InterceptionProfile(),
		langfusecontract.InterceptionProfile(),
	}
}

func ScrubEnv(env []string, credentialHelperPath string, socketPath string, sessionID string, bindFD int, sessionRepo string, ghWrapperDir string, realGhPath string) []string {
	session := interception.Session{
		Repo:                 sessionRepo,
		CredentialHelperPath: credentialHelperPath,
	}
	result := interception.Apply(env, interceptionProfiles(), session)

	result = append(result, "AI_AGENT_AUTH_SOCK="+socketPath)
	result = append(result, "AI_AGENT_SESSION_ID="+sessionID)
	result = append(result, "AI_AGENT_SESSION_BIND_FD="+strconv.Itoa(bindFD))
	result = append(result, "AI_AGENT_SESSION_REPO="+sessionRepo)
	if realGhPath != "" {
		result = append(result, "AI_AGENT_REAL_GH="+realGhPath)
	}

	if ghWrapperDir != "" {
		result = prependPath(result, ghWrapperDir)
	}

	return result
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
