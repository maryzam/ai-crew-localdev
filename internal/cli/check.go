package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/quality"
	"github.com/spf13/cobra"
)

var (
	checkDir            string
	checkKeepSuccessLog bool
	checkTailLines      int
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

	result, err := quality.RunCheck(quality.CheckOptions{
		Command:        args,
		Dir:            checkDir,
		EvidenceDir:    filepath.Join(paths.ConfigDir(), "evidence"),
		KeepSuccessLog: checkKeepSuccessLog,
		TailLines:      checkTailLines,
		Stdin:          os.Stdin,
	})
	if err != nil {
		return err
	}

	if result.EvidenceCleanupErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "check: evidence cleanup failed: %v\n", result.EvidenceCleanupErr)
	}

	if result.Passed {
		if result.LogPath != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "check: passed duration=%s log=%s\n", result.Duration, result.LogPath)
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "check: passed duration=%s\n", result.Duration)
		}
		return nil
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "check: failed exit=%d duration=%s log=%s", result.ExitCode, result.Duration, result.LogPath)
	if result.Truncated {
		_, _ = fmt.Fprint(cmd.ErrOrStderr(), " truncated=true")
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr())
	printOutputTail(cmd.ErrOrStderr(), result.FailureTail)
	exitProcess(result.ExitCode)
	return nil
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
