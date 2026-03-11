package launcher

// ScrubbedEnvVars is the canonical list of environment variables that must be
// unset or cleared before launching an agent session. These variables could
// bypass brokered auth or leak ambient credentials.
//
// See: docs/ai-agent-auth-architecture.md § Fail-Closed Controls
var ScrubbedEnvVars = []string{
	// GitHub token variables — could bypass broker-minted tokens.
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"GH_HOST",

	// SSH agent — could provide SSH key auth bypassing HTTPS broker path.
	"SSH_AUTH_SOCK",
	"GIT_SSH",
	"GIT_SSH_COMMAND",
	"SSH_ASKPASS",

	// Git credential helpers — could inject stored credentials.
	"GIT_ASKPASS",

	// Git config that might embed credentials or override helpers.
	"GIT_CONFIG_GLOBAL",
	"GIT_CONFIG_SYSTEM",

	// Any existing GIT_CONFIG_COUNT chain from the parent — we set our own.
	"GIT_CONFIG_COUNT",
}

// ForcedEnvVars are environment variables that must be set in the agent
// process tree to enforce fail-closed behavior.
var ForcedEnvVars = map[string]string{
	// Disable interactive credential prompts so git fails closed
	// instead of prompting the user when the broker is unavailable.
	"GIT_TERMINAL_PROMPT": "0",
}

// ScrubEnv returns a new copy of the environment with ambient credentials
// removed and fail-closed variables injected. It also sets up git credential
// helper configuration via GIT_CONFIG_COUNT.
func ScrubEnv(env []string, credentialHelperPath string, socketPath string, sessionID string, bindFD int) []string {
	// Build set of vars to scrub.
	scrubSet := make(map[string]bool, len(ScrubbedEnvVars))
	for _, v := range ScrubbedEnvVars {
		scrubSet[v] = true
	}

	// Also scrub any existing GIT_CONFIG_KEY_*/GIT_CONFIG_VALUE_* from parent.
	for _, e := range env {
		for _, prefix := range []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"} {
			if len(e) > len(prefix) && e[:len(prefix)] == prefix {
				idx := indexOf(e, '=')
				if idx > 0 {
					scrubSet[e[:idx]] = true
				}
			}
		}
	}

	// Filter environment.
	result := make([]string, 0, len(env))
	for _, e := range env {
		idx := indexOf(e, '=')
		if idx <= 0 {
			continue
		}
		key := e[:idx]
		if scrubSet[key] {
			continue
		}
		result = append(result, e)
	}

	// Add forced variables.
	for k, v := range ForcedEnvVars {
		result = append(result, k+"="+v)
	}

	// Add session metadata.
	result = append(result, "AI_AGENT_AUTH_SOCK="+socketPath)
	result = append(result, "AI_AGENT_SESSION_ID="+sessionID)
	result = append(result, "AI_AGENT_SESSION_BIND_FD="+itoa(bindFD))

	// Set up git credential helper via environment-backed config.
	// GIT_CONFIG_COUNT=2:
	//   0: credential.helper = <path>
	//   1: credential.helper =       (empty, clears any previously configured helpers)
	//
	// Note: git evaluates credential.helper entries in order. An empty value
	// resets the list. We put the empty value first to clear defaults, then
	// add our helper.
	result = append(result,
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=credential.helper",
		"GIT_CONFIG_VALUE_1="+credentialHelperPath,
	)

	return result
}

func indexOf(s string, b byte) int {
	for i := range len(s) {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
