package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/outputlimit"
	"github.com/spf13/cobra"
)

const (
	maxEvidenceLogBytes = 10 * 1024 * 1024
	maxEvidenceDirBytes = 20 * 1024 * 1024
	maxEvidenceFiles    = 20
	maxFailureTailBytes = 256 * 1024
)

var (
	checkDir            string
	checkKeepSuccessLog bool
	checkTailLines      int
	checkExecCommand    = exec.Command
)

var checkCmd = &cobra.Command{
	Use:          "check [flags] -- <command> [args...]",
	Short:        "Run a command with bounded output and local failure evidence",
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	RunE:         runCheck,
}

func init() {
	checkCmd.Flags().StringVar(&checkDir, "dir", "", "working directory")
	checkCmd.Flags().BoolVar(&checkKeepSuccessLog, "keep-success-log", false, "keep output when the command passes")
	checkCmd.Flags().IntVar(&checkTailLines, "tail-lines", 60, "maximum failure lines to print")
}

func runCheck(cmd *cobra.Command, args []string) error {
	if checkTailLines < 0 || checkTailLines > 1000 {
		return fmt.Errorf("--tail-lines must be between 0 and 1000")
	}

	evidenceDir := filepath.Join(config.ConfigDir(), "evidence")
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		return fmt.Errorf("create evidence directory: %w", err)
	}
	logFile, err := os.CreateTemp(evidenceDir, "check-*.log")
	if err != nil {
		return fmt.Errorf("create evidence log: %w", err)
	}
	logPath := logFile.Name()
	defer func() { _ = logFile.Close() }()

	output := outputlimit.New(maxEvidenceLogBytes)
	child := checkExecCommand(args[0], args[1:]...)
	child.Stdin = os.Stdin
	child.Stdout = output
	child.Stderr = output
	if checkDir != "" {
		child.Dir = checkDir
	}

	started := time.Now()
	runErr := child.Run()
	duration := time.Since(started).Round(time.Millisecond)
	if _, err := logFile.Write(output.Bytes()); err != nil {
		return fmt.Errorf("write evidence log: %w", err)
	}
	if err := logFile.Close(); err != nil {
		return fmt.Errorf("close evidence log: %w", err)
	}

	if runErr == nil && !checkKeepSuccessLog {
		_ = os.Remove(logPath)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "check: passed duration=%s\n", duration)
		return nil
	}
	if err := pruneEvidence(evidenceDir, logPath); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "check: evidence cleanup failed: %v\n", err)
	}
	if runErr == nil {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "check: passed duration=%s log=%s\n", duration, logPath)
		return nil
	}

	exitCodeValue := commandExitCode(runErr)
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "check: failed exit=%d duration=%s log=%s", exitCodeValue, duration, logPath)
	if output.Truncated() {
		_, _ = fmt.Fprint(cmd.ErrOrStderr(), " truncated=true")
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr())
	printOutputTail(cmd.ErrOrStderr(), output.LastLines(checkTailLines, maxFailureTailBytes))
	exitProcess(exitCodeValue)
	return nil
}

func commandExitCode(err error) int {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 1
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	if code := exitErr.ExitCode(); code >= 0 {
		return code
	}
	return 1
}

func printOutputTail(writer interface{ Write([]byte) (int, error) }, lines []string) {
	if len(lines) == 0 {
		return
	}
	_, _ = fmt.Fprintln(writer, "--- failure tail ---")
	for _, line := range lines {
		_, _ = fmt.Fprintln(writer, line)
	}
}

type evidenceFile struct {
	path    string
	size    int64
	modTime time.Time
}

func pruneEvidence(dir, keep string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	files := make([]evidenceFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, evidenceFile{path: filepath.Join(dir, entry.Name()), size: info.Size(), modTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })
	var total int64
	kept := 0
	for _, file := range files {
		mustKeep := file.path == keep
		if !mustKeep && (kept >= maxEvidenceFiles || total+file.size > maxEvidenceDirBytes) {
			if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		total += file.size
		kept++
	}
	return nil
}
