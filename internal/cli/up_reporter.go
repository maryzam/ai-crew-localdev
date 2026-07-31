package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/uphost"
)

const (
	glyphStep = "▸"
	glyphOK   = "✓"
	glyphWarn = "!"
	glyphFail = "✗"

	upLogTailLines     = 40
	upLogTailLineBytes = 4096
	upLogRetention     = 5
	upLogMaxBytes      = 10 << 20
	upLogPrefix        = "up-"
	upLogSuffix        = ".log"
)

var upLogSequence atomic.Uint64

type upReporter struct {
	out     io.Writer
	errOut  io.Writer
	verbose bool
	color   bool
	logPath string
	logFile *os.File
	tail    *tailBuffer
	capture io.Writer
	failed  bool
}

func newUpReporter(cmd *cobra.Command, verbose bool) (*upReporter, error) {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	logDir := filepath.Join(paths.DataDir(), "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	logName := fmt.Sprintf("%s%d-%d%s", upLogPrefix, time.Now().UnixNano(), upLogSequence.Add(1), upLogSuffix)
	logPath := filepath.Join(logDir, logName)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open up log %s: %w", logPath, err)
	}
	pruneUpLogs(logDir, upLogRetention)
	tail := newTailBuffer(upLogTailLines, upLogTailLineBytes)
	writers := []io.Writer{newCappedWriter(logFile, upLogMaxBytes), tail}
	if verbose {
		writers = append(writers, out)
	}
	return &upReporter{
		out:     out,
		errOut:  errOut,
		verbose: verbose,
		color:   useColor(out),
		logPath: logPath,
		logFile: logFile,
		tail:    tail,
		capture: io.MultiWriter(writers...),
	}, nil
}

func (r *upReporter) commandWriter() io.Writer { return r.capture }

func (r *upReporter) progressFunc() uphost.ProgressFunc {
	return func(p uphost.Progress) { r.renderProgress(p) }
}

func (r *upReporter) Close() {
	if r.logFile != nil {
		_ = r.logFile.Close()
	}
}

func (r *upReporter) renderProgress(p uphost.Progress) {
	switch p.Kind {
	case uphost.GenericLaunching:
		r.step("Launching devcontainer (%s) with %s", p.Target, p.Runtime)
	case uphost.GenericReady:
		r.ok("Devcontainer ready — workspace %s mounted at /workspace", p.Workspace)
		r.detail("re-enter later: %s", p.Command)
		if p.Repo != "" {
			r.detail("landed in %s — start a session: ai-agent run --agent <agent> --repo . -- <agent>", p.Repo)
		} else {
			r.detail("to start working: cd into your repo, then run: ai-agent run --agent <agent> --repo . -- <agent>")
		}
		r.detail("agent login persists in /home/dev; check it with 'ai-agent auth status' inside the container")
		r.detail("run git and gh through 'ai-agent run'; do not run 'gh auth login' here")
	case uphost.ProjectLaunching:
		r.step("Launching project devcontainer (%s) with %s", p.Target, p.Runtime)
	case uphost.ProjectBootstrapFailed:
		r.logDetail("project bootstrap", p.Err)
		r.warn("Optional agent defaults were not installed (details in %s)", r.logPath)
	case uphost.ProjectReady:
		r.ok("Project devcontainer ready — broker and ai-agent toolchain injected")
		r.detail("re-enter later: %s", p.Command)
		r.detail("agent login persists in /home/dev; check it with 'ai-agent auth status' inside the container")
	case uphost.AuthStatusChecking:
		r.step("Checking agent login state")
	case uphost.AuthStatusFailed:
		r.logDetail("auth status probe", p.Err)
		r.warn("Couldn't check agent login automatically — run 'ai-agent auth status' in the shell to sign in")
	case uphost.ShellOpening:
		r.step("Opening shell")
	case uphost.LangfuseEnvironment:
		r.detail("langfuse: created .env from .env.example (review secrets before production use)")
	case uphost.LangfuseStarting:
		r.step("Starting Langfuse observability stack")
	case uphost.LangfuseReady:
		r.ok("Langfuse ready at http://localhost:3000")
	}
}

func (r *upReporter) fail(message string, err error) error {
	if err == nil || r.failed {
		return err
	}
	r.failed = true
	r.logDetail(message, err)
	_, _ = fmt.Fprintf(r.errOut, "%s %s\n", r.paint("31", glyphFail), message)
	lines := r.tail.tail()
	if len(lines) == 0 {
		_, _ = fmt.Fprintf(r.errOut, "    full log: %s\n", r.logPath)
		return err
	}
	_, _ = fmt.Fprintf(r.errOut, "    last output (full log: %s):\n", r.logPath)
	for _, line := range lines {
		_, _ = fmt.Fprintf(r.errOut, "    | %s\n", line)
	}
	return err
}

func (r *upReporter) step(format string, args ...any) {
	_, _ = fmt.Fprintf(r.out, "%s %s\n", r.paint("36", glyphStep), fmt.Sprintf(format, args...))
}

func (r *upReporter) ok(format string, args ...any) {
	_, _ = fmt.Fprintf(r.out, "%s %s\n", r.paint("32", glyphOK), fmt.Sprintf(format, args...))
}

func (r *upReporter) warn(format string, args ...any) {
	_, _ = fmt.Fprintf(r.errOut, "%s %s\n", r.paint("33", glyphWarn), fmt.Sprintf(format, args...))
}

func (r *upReporter) detail(format string, args ...any) {
	_, _ = fmt.Fprintf(r.out, "    %s\n", fmt.Sprintf(format, args...))
}

func (r *upReporter) logDetail(context string, err error) {
	if err == nil || r.logFile == nil {
		return
	}
	_, _ = fmt.Fprintf(r.logFile, "[%s] %v\n", context, err)
}

func (r *upReporter) paint(code, text string) string {
	if !r.color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func useColor(out io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTerminal(out)
}

func isTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	return err == nil
}

func pruneUpLogs(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, upLogPrefix) && strings.HasSuffix(name, upLogSuffix) {
			names = append(names, name)
		}
	}
	if len(names) <= keep {
		return
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-keep] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

type cappedWriter struct {
	sink      io.Writer
	remaining int64
	marker    string
	truncated bool
}

func newCappedWriter(sink io.Writer, budget int64) *cappedWriter {
	return &cappedWriter{
		sink:      sink,
		remaining: budget,
		marker:    fmt.Sprintf("\n[log truncated: exceeded %d-byte per-run budget]\n", budget),
	}
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	chunk := p
	if int64(len(chunk)) > c.remaining {
		chunk = chunk[:c.remaining]
	}
	if len(chunk) > 0 {
		if _, err := c.sink.Write(chunk); err != nil {
			return 0, err
		}
		c.remaining -= int64(len(chunk))
	}
	if len(chunk) < len(p) && !c.truncated {
		c.truncated = true
		_, _ = io.WriteString(c.sink, c.marker)
	}
	return len(p), nil
}

type tailBuffer struct {
	maxLines     int
	maxLineBytes int
	lines        []string
	partial      []byte
}

func newTailBuffer(maxLines, maxLineBytes int) *tailBuffer {
	return &tailBuffer{maxLines: maxLines, maxLineBytes: maxLineBytes}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	written := len(p)
	for {
		index := bytes.IndexByte(p, '\n')
		if index < 0 {
			t.growPartial(p)
			break
		}
		t.growPartial(p[:index])
		t.push(string(t.partial))
		t.partial = t.partial[:0]
		p = p[index+1:]
	}
	return written, nil
}

func (t *tailBuffer) growPartial(chunk []byte) {
	room := t.maxLineBytes - len(t.partial)
	if room <= 0 {
		return
	}
	if len(chunk) > room {
		chunk = chunk[:room]
	}
	t.partial = append(t.partial, chunk...)
}

func (t *tailBuffer) push(line string) {
	t.lines = append(t.lines, line)
	if len(t.lines) > t.maxLines {
		t.lines = t.lines[len(t.lines)-t.maxLines:]
	}
}

func (t *tailBuffer) tail() []string {
	out := append([]string(nil), t.lines...)
	if len(t.partial) > 0 {
		out = append(out, string(t.partial))
	}
	return out
}
