package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/app/agentauth"
	"github.com/spf13/cobra"
)

func newAuthCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "auth",
		Short: "Inspect agent CLI login state",
	}
	command.AddCommand(newAuthStatusCommand())
	return command
}

type authStatusOptions struct {
	json bool
}

func newAuthStatusCommand() *cobra.Command {
	options := authStatusOptions{}
	command := &cobra.Command{
		Use:   "status",
		Short: "Report Claude and Codex login state and how to sign in",
		Long: `Reports whether the Claude and Codex CLIs have persisted login state and how
to remediate a missing login.

Run this inside the devcontainer, where the agent CLIs and the persistent
/home/dev volume are available. Login state persists across container
replacement; GitHub access stays on the brokered path.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	command.Flags().BoolVar(&options.json, "json", false, "emit machine-readable JSON output")
	command.RunE = func(command *cobra.Command, args []string) error {
		return runAuthStatus(command, options)
	}
	return command
}

func runAuthStatus(cmd *cobra.Command, options authStatusOptions) error {
	service := agentauth.New(agentauth.Dependencies{
		FindBinary: exec.LookPath,
		Run:        runAuthProbe,
	})
	report := service.Status(commandContext(cmd))
	if options.json {
		return writeAuthJSON(cmd.OutOrStdout(), report)
	}
	writeAuthText(cmd.OutOrStdout(), report)
	return nil
}

const probeWaitDelay = time.Second

func runAuthProbe(ctx context.Context, name string, args ...string) agentauth.ProbeResult {
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = probeWaitDelay
	stdout := &cappedBuffer{limit: agentauth.MaxProbeOutput}
	stderr := &cappedBuffer{limit: agentauth.MaxProbeOutput}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	result := agentauth.ProbeResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.TimedOut = true
	case err != nil:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.Err = err
		}
	}
	return result
}

type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := c.limit - c.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			c.buf.Write(p[:remaining])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) Bytes() []byte {
	return c.buf.Bytes()
}

func writeAuthText(w io.Writer, report agentauth.Report) {
	_, _ = fmt.Fprintln(w, "ai-agent auth status")
	for _, agent := range report.Agents {
		_, _ = fmt.Fprintf(w, "[%s] %s%s\n", agent.Status, agent.Agent, authMethodSuffix(agent))
		if agent.Detail != "" {
			_, _ = fmt.Fprintf(w, "  detail: %s\n", agent.Detail)
		}
		if agent.Remediation != "" {
			_, _ = fmt.Fprintf(w, "  fix: %s\n", agent.Remediation)
		}
	}
	if report.AllLoggedIn() {
		_, _ = fmt.Fprintln(w, "all agents have persisted login state")
		return
	}
	_, _ = fmt.Fprintln(w, "some agents need login; personal login state persists in /home/dev, GitHub access stays brokered")
}

func authMethodSuffix(agent agentauth.AgentReport) string {
	if agent.Method == "" {
		return ""
	}
	if agent.Source != "" {
		return fmt.Sprintf(": %s (%s)", agent.Method, agent.Source)
	}
	return fmt.Sprintf(": %s", agent.Method)
}

func writeAuthJSON(w io.Writer, report agentauth.Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
