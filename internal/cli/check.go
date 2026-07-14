package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/quality"
	"github.com/spf13/cobra"
)

type checkOptions struct {
	dir            string
	keepSuccessLog bool
	tailLines      int
}

func newCheckCommand() *cobra.Command {
	options := checkOptions{tailLines: 60}
	command := &cobra.Command{
		Use:          "check [flags] -- <command> [args...]",
		Short:        "Run a command with bounded output and local failure evidence",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd, options, args)
		},
	}
	command.Flags().StringVar(&options.dir, "dir", options.dir, "working directory")
	command.Flags().BoolVar(&options.keepSuccessLog, "keep-success-log", options.keepSuccessLog, "keep output when the command passes")
	command.Flags().IntVar(&options.tailLines, "tail-lines", options.tailLines, "maximum failure lines to print")
	return command
}

func runCheck(cmd *cobra.Command, options checkOptions, args []string) error {
	if options.tailLines < 0 || options.tailLines > 1000 {
		return fmt.Errorf("--tail-lines must be between 0 and 1000")
	}

	result, err := quality.RunCheck(quality.CheckOptions{
		Command:        args,
		Dir:            options.dir,
		EvidenceDir:    filepath.Join(paths.ConfigDir(), "evidence"),
		KeepSuccessLog: options.keepSuccessLog,
		TailLines:      options.tailLines,
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
