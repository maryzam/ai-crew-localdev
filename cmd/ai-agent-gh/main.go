// ai-agent-gh is a wrapper around the gh CLI that obtains a brokered token
// before executing gh.
//
// It clears ambient GH_TOKEN/GITHUB_TOKEN, requests a scoped token from the
// broker, and sets GH_TOKEN and GITHUB_TOKEN only for the gh child process.
//
// The wrapper extracts -R owner/repo from the argument vector for repo
// validation against the session-bound repo. All other arguments are passed
// through to gh unmodified.
//
// Usage:
//
//	ai-agent-gh <gh-args...>
//
// Environment (set by ai-agent run):
//
//	AI_AGENT_AUTH_SOCK          - broker socket path
//	AI_AGENT_SESSION_ID         - session identifier
//	AI_AGENT_SESSION_BIND_FD    - file descriptor for bind secret
//	AI_AGENT_REAL_GH            - path to real gh binary (optional)
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/brokerclient"
	"github.com/maryzam/ai-crew-localdev/internal/launcher"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ai-agent-gh: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ghArgs := os.Args[1:]

	// Read session metadata.
	socketPath := os.Getenv("AI_AGENT_AUTH_SOCK")
	if socketPath == "" {
		return fmt.Errorf("AI_AGENT_AUTH_SOCK not set; not in a managed session")
	}

	sessionID := os.Getenv("AI_AGENT_SESSION_ID")
	if sessionID == "" {
		return fmt.Errorf("AI_AGENT_SESSION_ID not set; not in a managed session")
	}

	bindFDStr := os.Getenv("AI_AGENT_SESSION_BIND_FD")
	if bindFDStr == "" {
		return fmt.Errorf("AI_AGENT_SESSION_BIND_FD not set; not in a managed session")
	}
	bindFD, err := strconv.Atoi(bindFDStr)
	if err != nil {
		return fmt.Errorf("invalid AI_AGENT_SESSION_BIND_FD: %w", err)
	}

	// Read bind secret.
	bindSecret, err := launcher.ReadBindSecret(bindFD)
	if err != nil {
		return fmt.Errorf("read bind secret: %w", err)
	}

	// Determine repo from -R flag or session-bound fallback.
	repo := extractRepoFlag(ghArgs)
	if repo == "" {
		repo = os.Getenv("AI_AGENT_SESSION_REPO")
	}
	if repo == "" {
		return fmt.Errorf("cannot determine repo: use -R owner/repo or ensure AI_AGENT_SESSION_REPO is set")
	}

	// Request token from broker.
	client := &brokerclient.Client{SocketPath: socketPath}
	resp, err := client.MintToken(broker.TokenRequest{
		SessionID:  sessionID,
		BindSecret: bindSecret,
		Repo:       repo,
	})
	if err != nil {
		return fmt.Errorf("mint token: %w", err)
	}

	// Find real gh binary.
	ghPath, err := findRealGh()
	if err != nil {
		return err
	}

	// Build environment: scrub tokens, then set fresh ones.
	env := scrubGhEnv(os.Environ())
	env = append(env,
		"GH_TOKEN="+resp.Token,
		"GITHUB_TOKEN="+resp.Token,
	)

	// Exec real gh.
	argv := append([]string{ghPath}, ghArgs...)
	return syscall.Exec(ghPath, argv, env)
}

// extractRepoFlag extracts the -R or --repo value from gh arguments.
// Returns empty string if not found (broker will use session-bound repo).
func extractRepoFlag(args []string) string {
	for i, arg := range args {
		// -R owner/repo or --repo owner/repo
		if (arg == "-R" || arg == "--repo") && i+1 < len(args) {
			return args[i+1]
		}
		// -Rowner/repo
		if strings.HasPrefix(arg, "-R") && len(arg) > 2 {
			return arg[2:]
		}
		// --repo=owner/repo
		if strings.HasPrefix(arg, "--repo=") {
			return arg[7:]
		}
	}
	return ""
}

// findRealGh locates the real gh binary, skipping ourselves.
func findRealGh() (string, error) {
	// Check explicit override.
	if p := os.Getenv("AI_AGENT_REAL_GH"); p != "" {
		if err := validateExecutableFile(p); err != nil {
			return "", fmt.Errorf("AI_AGENT_REAL_GH=%s is invalid: %w", p, err)
		}
		return p, nil
	}

	selfInfo, selfErr := os.Stat("/proc/self/exe")

	path := os.Getenv("PATH")
	for _, dir := range strings.Split(path, ":") {
		candidate := dir + "/gh"
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}

		if selfErr == nil && os.SameFile(info, selfInfo) {
			continue
		}

		// Check it's executable.
		if info.Mode()&0111 != 0 {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("gh not found in PATH; install it or set AI_AGENT_REAL_GH")
}

func validateExecutableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory")
	}
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("path is not executable")
	}
	return nil
}

// scrubGhEnv removes token-related variables from the environment.
func scrubGhEnv(env []string) []string {
	scrub := map[string]bool{
		"GH_TOKEN":     true,
		"GITHUB_TOKEN": true,
		"GH_HOST":      true,
	}

	result := make([]string, 0, len(env))
	for _, e := range env {
		idx := strings.IndexByte(e, '=')
		if idx <= 0 {
			continue
		}
		if scrub[e[:idx]] {
			continue
		}
		result = append(result, e)
	}
	return result
}
