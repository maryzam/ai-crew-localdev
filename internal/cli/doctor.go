package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/doctor"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run pre-flight diagnostics for the broker",
	RunE:  runDoctor,
}

var doctorConfigDir string

func init() {
	doctorCmd.Flags().StringVar(&doctorConfigDir, "config-dir", "", "path to configuration directory")
}

func runDoctor(cmd *cobra.Command, args []string) error {
	cfgDir := doctorConfigDir
	if cfgDir == "" {
		cfgDir = config.ConfigDir()
	}
	cfgDir = config.ExpandHome(cfgDir)

	runner := &doctor.Runner{
		ConfigDir:  cfgDir,
		RuntimeDir: config.RuntimeDir(),
		Stdout:     cmd.OutOrStdout(),
		Stderr:     cmd.OutOrStderr(),
	}

	results := runner.RunAll()
	ok := runner.PrintResults(results)

	if !ok {
		os.Exit(1)
	}
	return nil
}
