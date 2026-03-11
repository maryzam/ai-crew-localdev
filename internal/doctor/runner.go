package doctor

import (
	"fmt"
	"io"
)

// Runner executes all diagnostic checks and formats output.
type Runner struct {
	ConfigDir  string
	RuntimeDir string
	Stdout     io.Writer
	Stderr     io.Writer
}

// RunAll executes all diagnostic checks and returns the collected results.
func (r *Runner) RunAll() []CheckResult {
	var results []CheckResult

	results = append(results, CheckIdentitiesFile(r.ConfigDir))
	results = append(results, CheckPEMFiles(r.ConfigDir)...)
	results = append(results, CheckAppIDs(r.ConfigDir))
	results = append(results, CheckPolicyFile(r.ConfigDir))
	results = append(results, CheckBrokerSocketDir(r.RuntimeDir))
	results = append(results, CheckSystemdUser())
	results = append(results, CheckAllowedRepos(r.ConfigDir))

	return results
}

// PrintResults formats and prints the check results. Returns true if there are
// no failures (all results are pass or warn).
func (r *Runner) PrintResults(results []CheckResult) bool {
	fmt.Fprintln(r.Stdout, "ai-agent doctor")

	passed := 0
	warned := 0
	failed := 0

	for _, res := range results {
		var marker string
		switch res.Status {
		case StatusPass:
			marker = "\u2713" // check mark
			passed++
		case StatusWarn:
			marker = "\u26a0" // warning sign
			warned++
		case StatusFail:
			marker = "\u2717" // cross mark
			failed++
		}

		fmt.Fprintf(r.Stdout, "  %s %s\n", marker, res.Message)
		if res.Detail != "" && res.Status != StatusPass {
			fmt.Fprintf(r.Stdout, "    %s\n", res.Detail)
		}
	}

	fmt.Fprintln(r.Stdout)
	fmt.Fprintf(r.Stdout, "%d passed, %d warning, %d failed\n", passed, warned, failed)

	return failed == 0
}
