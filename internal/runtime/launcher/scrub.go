package launcher

import (
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"strconv"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/interception"
	"github.com/maryzam/ai-crew-localdev/internal/providers/profiles"
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

func interceptionProfiles() []interception.Profile {
	return append(
		[]interception.Profile{{Provider: "session", ScrubEnv: sessionEnvVars}},
		profiles.All()...,
	)
}

func ScrubEnv(env []string, credentialHelperPath string, socketPath string, sessionID string, bindFD int, sessionRepo string, ghWrapperDir string, realGhPath string) []string {
	session := interception.Session{
		Repo:                 sessionRepo,
		CredentialHelperPath: credentialHelperPath,
	}
	result := interception.Apply(env, interceptionProfiles(), session)

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

func prependPath(env []string, dir string) []string {
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + dir + ":" + e[5:]
			return env
		}
	}
	return append(env, "PATH="+dir)
}
