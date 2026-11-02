package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/maryzam/ai-crew-localdev/internal/product/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/product/paths"
	"github.com/maryzam/ai-crew-localdev/internal/product/telemetry"
	"github.com/maryzam/ai-crew-localdev/internal/product/wirecapture"
	"github.com/spf13/cobra"
)

func openEvidence() (*evidence.Root, error) {
	path, err := paths.Evidence()
	if err != nil {
		return nil, err
	}
	return evidence.Open(path)
}

func newLogsCommand(out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "logs [run-id]",
		Short: "Show a run's stored telemetry, usage, and any loss",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			root, err := openEvidence()
			if err != nil {
				return err
			}
			run, err := selectRun(root, args)
			if err != nil {
				return err
			}
			summary, err := telemetry.Summarize(root, run)
			if err != nil {
				return err
			}
			wire, err := wirecapture.Summarize(root, run)
			if err != nil {
				return err
			}
			return renderLogs(out, summary, wire)
		},
	}
}

func selectRun(root *evidence.Root, args []string) (string, error) {
	if len(args) == 0 {
		return root.LatestRun()
	}
	runs, err := root.Runs()
	if err != nil {
		return "", err
	}
	if !slices.Contains(runs, args[0]) {
		return "", fmt.Errorf("no evidence for run %q", args[0])
	}
	return args[0], nil
}

func renderLogs(out io.Writer, summary telemetry.Summary, wire wirecapture.Summary) error {
	var report strings.Builder
	fmt.Fprintf(&report, "Run %s\n", summary.Run)
	fmt.Fprintf(&report, "Telemetry: %d bytes; %d logs, %d metrics, %d spans", summary.TelemetryBytes, summary.Records["logs"], summary.Records["metrics"], summary.Records["spans"])
	if summary.Malformed > 0 {
		fmt.Fprintf(&report, "; %d unreadable lines", summary.Malformed)
	}
	report.WriteString("\n")
	if summary.Receiver != nil {
		fmt.Fprintf(&report, "Receiver: %d stored, %d rejected, %d failed\n", summary.Receiver.Stored, summary.Receiver.Rejected, summary.Receiver.Failed)
	} else {
		report.WriteString("Receiver: no counters recorded\n")
	}
	report.WriteString(wireLine(wire))
	if len(summary.Usage) > 0 {
		report.WriteString("\nUsage:\n")
		table := tabwriter.NewWriter(&report, 2, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(table, "AGENT\tSESSION\tMODEL\tREQUESTS\tINPUT\tOUTPUT\tCACHE_READ\tCACHE_WRITE\tREASONING\tCACHE_HIT\tEST_COST_USD")
		for _, usage := range summary.Usage {
			_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", terminalSafe(usage.Agent), terminalSafe(usage.Session), terminalSafe(usage.Model), usage.Requests, count(usage.Input), count(usage.Output), count(usage.CacheRead), count(usage.CacheWrite), count(usage.Reasoning), share(usage.CacheHitShare()), money(usage.CostUSD))
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}
	for _, pruned := range summary.Pruned {
		fmt.Fprintf(&report, "Pruned: %s, %d bytes, at %s\n", pruned.File, pruned.Bytes, pruned.Time.Format("2006-01-02 15:04:05Z"))
	}
	_, err := io.WriteString(out, report.String())
	return err
}

func wireLine(wire wirecapture.Summary) string {
	state := "not recorded"
	if wire.Enabled != nil {
		state = onOff(*wire.Enabled)
	}
	line := fmt.Sprintf("Wire capture: %s; %d exchanges, %d incomplete, %d failed, %d bytes on the wire, %d bytes stored", state, wire.Exchanges, wire.Incomplete, wire.Failed, wire.WireBytes, wire.StoredBytes)
	if wire.Malformed > 0 {
		line += fmt.Sprintf(", %d unreadable lines", wire.Malformed)
	}
	return line + "\n"
}

func terminalSafe(value string) string {
	const limit = 128
	safe := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return '?'
		}
		return r
	}, value)
	if len(safe) > limit {
		return safe[:limit] + "..."
	}
	return safe
}

func count(value *int64) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatInt(*value, 10)
}

func share(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", *value*100)
}

func money(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.4f", *value)
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func newCaptureCommand(out io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:   "capture",
		Short: "Control full request and response capture for new runs",
	}
	report := func() error {
		settings, err := wirecapture.LoadSettings()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Wire capture is %s for new runs\n", onOff(settings.Enabled))
		return err
	}
	set := func(enabled bool) func(*cobra.Command, []string) error {
		return func(*cobra.Command, []string) error {
			if err := wirecapture.SaveSettings(wirecapture.Settings{Enabled: enabled}); err != nil {
				return err
			}
			return report()
		}
	}
	command.AddCommand(
		&cobra.Command{Use: "status", Short: "Show whether capture is on for new runs", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return report() }},
		&cobra.Command{Use: "on", Short: "Capture model traffic for new runs (the default)", Args: cobra.NoArgs, RunE: set(true)},
		&cobra.Command{Use: "off", Short: "Stop capturing model traffic for new runs", Args: cobra.NoArgs, RunE: set(false)},
		&cobra.Command{Use: "export [run-id]", Short: "Print a run's captured traffic as JSON lines", Args: cobra.MaximumNArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			root, err := openEvidence()
			if err != nil {
				return err
			}
			run, err := selectRun(root, args)
			if err != nil {
				return err
			}
			return exportWire(out, root, run)
		}},
	)
	return command
}

type exportedBody struct {
	Text   *string `json:"text,omitempty"`
	Base64 *string `json:"base64,omitempty"`
}

func exportWire(out io.Writer, root *evidence.Root, run string) error {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	return wirecapture.Each(root, run, func(exchange wirecapture.Exchange, request, response []byte) error {
		return encoder.Encode(struct {
			wirecapture.Exchange
			RequestBody  exportedBody `json:"request_body"`
			ResponseBody exportedBody `json:"response_body"`
		}{exchange, exportBody(request), exportBody(response)})
	})
}

func exportBody(content []byte) exportedBody {
	if utf8.Valid(content) {
		text := string(content)
		return exportedBody{Text: &text}
	}
	encoded := base64.StdEncoding.EncodeToString(content)
	return exportedBody{Base64: &encoded}
}
