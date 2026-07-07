package interception

import (
	"slices"
	"testing"
)

func TestApplyScrubsNamesAndPrefixes(t *testing.T) {
	profiles := []Profile{
		{Provider: "a", ScrubEnv: []string{"SECRET_A"}, ScrubEnvPrefixes: []string{"A_PREFIX_"}},
		{Provider: "b", ScrubEnv: []string{"SECRET_B"}},
	}
	env := []string{
		"HOME=/home/test",
		"SECRET_A=leak",
		"SECRET_B=leak",
		"A_PREFIX_0=leak",
		"A_PREFIX_1=leak",
		"=malformed",
		"NOEQUALS",
	}

	result := Apply(env, profiles, Session{})

	if !slices.Equal(result, []string{"HOME=/home/test"}) {
		t.Fatalf("Apply = %v, want only HOME to survive", result)
	}
}

func TestApplyAppendsFailClosedEnvWithSession(t *testing.T) {
	profile := Profile{
		Provider: "a",
		ScrubEnv: []string{"FORCED"},
		FailClosedEnv: func(s Session) []string {
			return []string{"FORCED=" + s.Repo + ":" + s.CredentialHelperPath}
		},
	}
	env := []string{"FORCED=ambient"}

	result := Apply(env, []Profile{profile}, Session{Repo: "o/r", CredentialHelperPath: "/helper"})

	if !slices.Equal(result, []string{"FORCED=o/r:/helper"}) {
		t.Fatalf("Apply = %v, want fail-closed value to replace ambient", result)
	}
}

func TestApplyUnionsScrubAcrossProfiles(t *testing.T) {
	profiles := []Profile{
		{Provider: "a", ScrubEnv: []string{"ONLY_A"}},
		{Provider: "b", ScrubEnv: []string{"ONLY_B"}},
	}
	env := []string{"ONLY_A=leak", "ONLY_B=leak", "KEEP=1"}

	result := Apply(env, profiles, Session{})

	if !slices.Equal(result, []string{"KEEP=1"}) {
		t.Fatalf("Apply = %v, want scrub union of every profile", result)
	}
}
