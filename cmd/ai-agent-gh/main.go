package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/broker/client"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
	"github.com/maryzam/ai-crew-localdev/internal/sessionauth"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ai-agent-gh: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ghArgs := os.Args[1:]

	session, err := sessionauth.Load()
	if err != nil {
		return err
	}
	if err := rejectPersistentAuthCommand(ghArgs); err != nil {
		return err
	}

	repo := extractRepoFlag(ghArgs)
	if repo == "" {
		repo = os.Getenv("AI_AGENT_SESSION_REPO")
	}
	if repo == "" {
		return fmt.Errorf("cannot determine repo: use -R owner/repo or ensure AI_AGENT_SESSION_REPO is set")
	}

	client := &client.Client{SocketPath: session.SocketPath}
	resp, err := client.MintCredential(api.CredentialRequest{
		SessionID:      session.SessionID,
		BindSecret:     session.BindSecret,
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:" + repo,
	})
	if err != nil {
		return fmt.Errorf("mint credential: %w", err)
	}

	var ghCred githubcontract.Credential
	if err := json.Unmarshal(resp.Credential, &ghCred); err != nil {
		return fmt.Errorf("decode github credential payload: %w", err)
	}

	ghPath, err := findRealGh()
	if err != nil {
		return err
	}

	env := scrubGhEnv(os.Environ())
	env = append(env,
		"GH_TOKEN="+ghCred.Token,
		"GITHUB_TOKEN="+ghCred.Token,
	)

	argv := append([]string{ghPath}, ghArgs...)
	return syscall.Exec(ghPath, argv, env)
}

func rejectPersistentAuthCommand(args []string) error {
	if len(args) < 2 || args[0] != "auth" {
		return nil
	}

	switch args[1] {
	case "login", "setup-git", "refresh":
		return fmt.Errorf("gh auth %s is disabled in managed sessions; GitHub repo access is brokered by ai-agent, so do not write personal gh credentials or credential-helper config to the agent home", args[1])
	default:
		return nil
	}
}

func extractRepoFlag(args []string) string {
	for i, arg := range args {
		if (arg == "-R" || arg == "--repo") && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "-R") && len(arg) > 2 {
			return arg[2:]
		}
		if strings.HasPrefix(arg, "--repo=") {
			return arg[7:]
		}
	}
	return ""
}

func findRealGh() (string, error) {
	if p := os.Getenv("AI_AGENT_REAL_GH"); p != "" {
		return validateExecutable(p)
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

		if info.Mode()&0111 != 0 {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("gh not found in PATH; install it or set AI_AGENT_REAL_GH")
}

func validateExecutable(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("AI_AGENT_REAL_GH=%s is not accessible: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("AI_AGENT_REAL_GH=%s is a directory, not an executable file", path)
	}
	if info.Mode()&0111 == 0 {
		return "", fmt.Errorf("AI_AGENT_REAL_GH=%s is not executable", path)
	}
	return filepath.Clean(path), nil
}

func scrubGhEnv(env []string) []string {
	scrub := map[string]bool{
		"GH_TOKEN":                true,
		"GITHUB_TOKEN":            true,
		"GH_ENTERPRISE_TOKEN":     true,
		"GITHUB_ENTERPRISE_TOKEN": true,
		"GH_HOST":                 true,
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
