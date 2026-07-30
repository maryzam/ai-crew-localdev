package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/runtime/uphost"
)

func newTestReporter(t *testing.T, verbose bool) (*upReporter, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("AI_AGENT_DATA_DIR", dataDir)
	var out, errOut bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	reporter, err := newUpReporter(cmd, verbose)
	if err != nil {
		t.Fatalf("newUpReporter: %v", err)
	}
	return reporter, &out, &errOut, reporter.logPath
}

func TestUpReporterCapturesCommandOutputAwayFromTerminal(t *testing.T) {
	reporter, out, _, logPath := newTestReporter(t, false)
	_, _ = fmt.Fprintln(reporter.commandWriter(), "podman: pulling layer sha256:deadbeef")
	reporter.Close()

	if strings.Contains(out.String(), "podman") {
		t.Fatalf("command noise leaked to terminal: %q", out.String())
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "podman: pulling layer") {
		t.Fatalf("log missing captured output: %q", string(logged))
	}
}

func TestUpReporterVerboseTeesCommandOutputToTerminal(t *testing.T) {
	reporter, out, _, _ := newTestReporter(t, true)
	_, _ = fmt.Fprintln(reporter.commandWriter(), "podman: pulling layer sha256:deadbeef")
	reporter.Close()

	if !strings.Contains(out.String(), "podman: pulling layer") {
		t.Fatalf("verbose mode should stream command output, got %q", out.String())
	}
}

func TestUpReporterSoftensAuthStatusFailure(t *testing.T) {
	reporter, _, errOut, logPath := newTestReporter(t, false)
	reporter.renderProgress(uphost.Progress{Kind: uphost.AuthStatusFailed, Err: errors.New("agent login status: exit status 127")})
	reporter.Close()

	got := errOut.String()
	if !strings.Contains(got, "Couldn't check agent login automatically") {
		t.Fatalf("missing actionable guidance: %q", got)
	}
	if strings.Contains(got, "127") || strings.Contains(got, "exit status") {
		t.Fatalf("raw exit code leaked to terminal: %q", got)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "exit status 127") {
		t.Fatalf("raw cause should be preserved in the log: %q", string(logged))
	}
}

func TestUpReporterFailCuratesMessageAndKeepsExitCodeInLog(t *testing.T) {
	reporter, _, errOut, logPath := newTestReporter(t, false)
	_, _ = fmt.Fprintln(reporter.commandWriter(), "step one ok")
	_, _ = fmt.Fprintln(reporter.commandWriter(), "fatal: something broke")
	returned := reporter.fail("Devcontainer launch failed", errors.New("devcontainer up: exit status 1"))
	reporter.Close()

	if returned == nil {
		t.Fatal("fail should return the error for a non-zero exit")
	}
	got := errOut.String()
	for _, want := range []string{"Devcontainer launch failed", "fatal: something broke", logPath} {
		if !strings.Contains(got, want) {
			t.Fatalf("failure output %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "exit status") {
		t.Fatalf("raw exit code leaked to the operator view: %q", got)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "exit status 1") {
		t.Fatalf("raw error should be preserved in the log: %q", string(logged))
	}
}

func TestUpReporterFailIsIdempotent(t *testing.T) {
	reporter, _, errOut, _ := newTestReporter(t, false)
	_ = reporter.fail("first failure", errors.New("first"))
	_ = reporter.fail("second failure", errors.New("second"))
	reporter.Close()
	if strings.Contains(errOut.String(), "second failure") {
		t.Fatalf("second failure should not re-surface: %q", errOut.String())
	}
}

func TestUpReporterNoColorForNonTerminal(t *testing.T) {
	reporter, out, _, _ := newTestReporter(t, false)
	reporter.step("Launching devcontainer")
	reporter.Close()
	got := out.String()
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("non-terminal output must be uncolored: %q", got)
	}
	if !strings.Contains(got, glyphStep) || !strings.Contains(got, "Launching devcontainer") {
		t.Fatalf("step line missing content: %q", got)
	}
}

func TestUpReporterGenericReadyShowsNextCommand(t *testing.T) {
	reporter, out, _, _ := newTestReporter(t, false)
	reporter.renderProgress(uphost.Progress{Kind: uphost.GenericReady, Workspace: "/home/me/github", Command: "devcontainer exec ..."})
	reporter.Close()
	if !strings.Contains(out.String(), "to start working") {
		t.Fatalf("generic-ready should show the next command hint: %q", out.String())
	}
}

func TestTailBufferKeepsLastLines(t *testing.T) {
	buffer := newTailBuffer(3, upLogTailLineBytes)
	for index := 0; index < 6; index++ {
		_, _ = fmt.Fprintf(buffer, "line %d\n", index)
	}
	_, _ = buffer.Write([]byte("partial without newline"))
	got := buffer.tail()
	want := []string{"line 3", "line 4", "line 5", "partial without newline"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tail = %v, want %v", got, want)
	}
}

func TestTailBufferBoundsLineAndPartialBytes(t *testing.T) {
	buffer := newTailBuffer(2, 8)
	_, _ = buffer.Write([]byte(strings.Repeat("A", 100) + "\n"))
	_, _ = buffer.Write([]byte(strings.Repeat("B", 100)))
	got := buffer.tail()
	if len(got) != 2 {
		t.Fatalf("tail = %v, want 2 entries", got)
	}
	for _, line := range got {
		if len(line) > 8 {
			t.Fatalf("line %q exceeds the byte budget", line)
		}
	}
}

func TestCappedWriterEnforcesByteBudget(t *testing.T) {
	var sink bytes.Buffer
	writer := newCappedWriter(&sink, 10)

	if written, err := writer.Write([]byte("0123456789ABCDEFGH")); err != nil || written != 18 {
		t.Fatalf("Write = (%d, %v), want (18, nil) so io.MultiWriter never short-writes", written, err)
	}
	if written, err := writer.Write([]byte("more output after the cap")); err != nil || written != 25 {
		t.Fatalf("post-cap Write = (%d, %v), want (25, nil)", written, err)
	}

	got := sink.String()
	if !strings.HasPrefix(got, "0123456789") {
		t.Fatalf("first %d bytes should be kept verbatim: %q", 10, got)
	}
	if strings.Contains(got, "ABCDEF") || strings.Contains(got, "more output") {
		t.Fatalf("bytes past the budget must be dropped: %q", got)
	}
	if !strings.Contains(got, "log truncated") {
		t.Fatalf("truncation marker missing: %q", got)
	}
	if int64(sink.Len()) > 10+int64(len(writer.marker)) {
		t.Fatalf("sink grew past budget + marker: %d bytes", sink.Len())
	}
}

func TestUpReporterUsesPerRunLogsWithRetention(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AI_AGENT_DATA_DIR", dataDir)
	logDir := filepath.Join(dataDir, "logs")

	var paths []string
	for run := 0; run < upLogRetention+3; run++ {
		cmd := &cobra.Command{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		reporter, err := newUpReporter(cmd, false)
		if err != nil {
			t.Fatalf("newUpReporter: %v", err)
		}
		_, _ = fmt.Fprintf(reporter.commandWriter(), "run %d evidence\n", run)
		reporter.Close()
		paths = append(paths, reporter.logPath)
	}

	if paths[0] == paths[len(paths)-1] {
		t.Fatal("each run must use a distinct log file")
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	kept := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), upLogPrefix) && strings.HasSuffix(entry.Name(), upLogSuffix) {
			kept++
		}
	}
	if kept != upLogRetention {
		t.Fatalf("retained %d logs, want %d", kept, upLogRetention)
	}
	if _, err := os.Stat(paths[len(paths)-1]); err != nil {
		t.Fatalf("most recent run log should survive retention: %v", err)
	}
}
