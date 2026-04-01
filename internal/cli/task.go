package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/launcher"
	"github.com/spf13/cobra"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage task-oriented worktrees",
}

var taskStartCmd = &cobra.Command{
	Use:   "start [flags] -- <agent-command> [args...]",
	Short: "Create a task worktree and launch an agent there",
	Long: `Creates a dedicated git worktree for a task, creates a task branch,
and then launches a managed ai-agent session from that worktree.

This keeps isolation task-oriented instead of agent-oriented: multiple tools
can collaborate on the same task branch, while unrelated tasks get separate
worktrees by default.

Examples:
  ai-agent task start --task-name "add billing webhook" --agent codex --repo . -- codex
  ai-agent task start --task-name "fix flaky test" --agent claude --repo . --base main -- claude`,
	SilenceUsage: true,
	RunE:         runTaskStart,
}

var (
	taskStartAgent        string
	taskStartTaskName     string
	taskStartRepo         string
	taskStartBase         string
	taskStartBranchName   string
	taskStartWorktreeRoot string
	taskStartSocketPath   string
	taskStartCredHelper   string
	taskStartGhWrapper    string
	taskStartVerifyCmd    string
	taskStartMaxRetries   int
)

var (
	taskExecCommand = exec.Command
	taskLaunch      = launcher.Launch
)

func init() {
	rootCmd.AddCommand(taskCmd)
	taskCmd.AddCommand(taskStartCmd)

	taskStartCmd.Flags().StringVar(&taskStartAgent, "agent", "", "agent identity name (required)")
	taskStartCmd.Flags().StringVar(&taskStartTaskName, "task-name", "", "human-readable task name used for branch and worktree naming (required)")
	taskStartCmd.Flags().StringVar(&taskStartRepo, "repo", ".", "path to the git repository")
	taskStartCmd.Flags().StringVar(&taskStartBase, "base", "origin/main", "base ref to branch from")
	taskStartCmd.Flags().StringVar(&taskStartBranchName, "branch-name", "", "explicit branch name override (default: task/<sanitized-task-name>)")
	taskStartCmd.Flags().StringVar(&taskStartWorktreeRoot, "worktree-root", "", "directory under which managed worktrees are created")
	taskStartCmd.Flags().StringVar(&taskStartSocketPath, "broker-sock", "", "broker socket path (default: auto)")
	taskStartCmd.Flags().StringVar(&taskStartCredHelper, "credential-helper", "", "path to credential helper binary (default: auto-detect)")
	taskStartCmd.Flags().StringVar(&taskStartGhWrapper, "gh-wrapper", "", "path to ai-agent-gh binary (default: auto-detect)")
	taskStartCmd.Flags().StringVar(&taskStartVerifyCmd, "verify-cmd", "", "shell command to run after agent exits (e.g. \"make test\"); enables verify-and-retry loop")
	taskStartCmd.Flags().IntVar(&taskStartMaxRetries, "max-retries", 2, "max retries when --verify-cmd fails")
	_ = taskStartCmd.MarkFlagRequired("agent")
	_ = taskStartCmd.MarkFlagRequired("task-name")
}

func runTaskStart(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no agent command specified; use -- to separate agent command from flags")
	}

	worktree, err := prepareTaskWorktree(taskStartRepo, taskStartTaskName, taskStartBase, taskStartBranchName, taskStartWorktreeRoot)
	if err != nil {
		return err
	}

	socketPath := resolveBrokerSocketPath(taskStartSocketPath)

	credHelper := taskStartCredHelper
	if credHelper == "" {
		credHelper, err = resolveOptionalBinary("ai-agent-credential-helper")
		if err != nil || credHelper == "" {
			return fmt.Errorf("ai-agent-credential-helper not found next to ai-agent or in PATH; install it or use --credential-helper")
		}
	}
	if _, err := os.Stat(credHelper); err != nil {
		return fmt.Errorf("credential helper not found at %s: %w", credHelper, err)
	}

	ghWrapper := taskStartGhWrapper
	if ghWrapper == "" {
		ghWrapper, _ = resolveOptionalBinary("ai-agent-gh")
	}

	realGhPath := ""
	if ghWrapper != "" {
		realGhPath = resolveRealGhPath(ghWrapper)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "task worktree ready: %s\nbranch: %s\nbase: %s\n",
		worktree.Path, worktree.BranchName, worktree.BaseRef)

	return taskLaunch(launcher.Options{
		AgentName:    taskStartAgent,
		RepoPath:     worktree.Path,
		SocketPath:   socketPath,
		CredHelper:   credHelper,
		GhWrapper:    ghWrapper,
		RealGhPath:   realGhPath,
		AgentCommand: args,
		VerifyCmd:    taskStartVerifyCmd,
		MaxRetries:   taskStartMaxRetries,
	})
}

type taskWorktree struct {
	RepoRoot   string
	Path       string
	BranchName string
	BaseRef    string
}

func prepareTaskWorktree(repoPath, taskName, baseRef, branchNameOverride, worktreeRoot string) (taskWorktree, error) {
	repoRoot, err := resolveRepoRoot(repoPath)
	if err != nil {
		return taskWorktree{}, err
	}

	taskSlug, err := sanitizeTaskName(taskName)
	if err != nil {
		return taskWorktree{}, err
	}

	branchName := branchNameOverride
	if branchName == "" {
		branchName = "task/" + taskSlug
	}
	if err := validateBranchName(repoRoot, branchName); err != nil {
		return taskWorktree{}, err
	}

	if err := maybeFetchBaseRef(repoRoot, baseRef); err != nil {
		return taskWorktree{}, err
	}

	rootDir, err := resolveManagedWorktreeRoot(repoRoot, worktreeRoot)
	if err != nil {
		return taskWorktree{}, err
	}
	worktreePath := filepath.Join(rootDir, taskSlug)
	if _, err := os.Stat(worktreePath); err == nil {
		return taskWorktree{}, fmt.Errorf("worktree path already exists: %s", worktreePath)
	} else if !os.IsNotExist(err) {
		return taskWorktree{}, fmt.Errorf("check worktree path %s: %w", worktreePath, err)
	}

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return taskWorktree{}, fmt.Errorf("create worktree parent: %w", err)
	}

	if out, err := runGit(repoRoot, "worktree", "add", "-b", branchName, worktreePath, baseRef); err != nil {
		return taskWorktree{}, fmt.Errorf("create git worktree: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return taskWorktree{
		RepoRoot:   repoRoot,
		Path:       worktreePath,
		BranchName: branchName,
		BaseRef:    baseRef,
	}, nil
}

func resolveRepoRoot(repoPath string) (string, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return "", fmt.Errorf("resolve repo path: %w", err)
	}

	out, err := runGit(absPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve repo root: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return strings.TrimSpace(string(out)), nil
}

func validateBranchName(repoRoot, branchName string) error {
	out, err := runGit(repoRoot, "check-ref-format", "--branch", branchName)
	if err != nil {
		return fmt.Errorf("invalid branch name %q: %w: %s", branchName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func maybeFetchBaseRef(repoRoot, baseRef string) error {
	if baseRefExists(repoRoot, baseRef) {
		return nil
	}

	remoteName := remoteNameFromBaseRef(baseRef)
	if remoteName == "" {
		return nil
	}

	out, err := runGit(repoRoot, "fetch", remoteName)
	if err != nil {
		return fmt.Errorf("fetch base ref %q: %w: %s", baseRef, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func baseRefExists(repoRoot, baseRef string) bool {
	if baseRef == "" {
		return false
	}

	_, err := runGit(repoRoot, "rev-parse", "--verify", "--quiet", baseRef+"^{commit}")
	return err == nil
}

func remoteNameFromBaseRef(baseRef string) string {
	if baseRef == "" || strings.HasPrefix(baseRef, "refs/") {
		return ""
	}

	parts := strings.SplitN(baseRef, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0]
}

func resolveManagedWorktreeRoot(repoRoot, override string) (string, error) {
	if override != "" {
		absOverride, err := filepath.Abs(override)
		if err != nil {
			return "", err
		}
		return filepath.Join(absOverride, filepath.Base(repoRoot)), nil
	}

	parent := filepath.Dir(repoRoot)
	return filepath.Join(parent, ".ai-agent-worktrees", filepath.Base(repoRoot)), nil
}

var taskNameSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func sanitizeTaskName(taskName string) (string, error) {
	slug := strings.ToLower(strings.TrimSpace(taskName))
	slug = taskNameSanitizer.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "", fmt.Errorf("task name %q does not contain any usable branch characters", taskName)
	}
	return slug, nil
}

func runGit(repoPath string, args ...string) ([]byte, error) {
	cmd := taskExecCommand("git", append([]string{"-C", repoPath}, args...)...)
	return cmd.CombinedOutput()
}
