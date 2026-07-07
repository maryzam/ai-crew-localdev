package quality

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/outputlimit"
)

const (
	MaxEvidenceLogBytes = 10 * 1024 * 1024
	maxEvidenceDirBytes = 20 * 1024 * 1024
	maxEvidenceFiles    = 20
	maxFailureTailBytes = 256 * 1024
)

var execCommand = exec.Command

type CheckOptions struct {
	Command        []string
	Dir            string
	Env            []string
	EvidenceDir    string
	KeepSuccessLog bool
	TailLines      int
	Stdin          io.Reader
	ExtraFiles     []*os.File
	Run            func(*exec.Cmd) error
}

const (
	FailureClassExit        = "exit"
	FailureClassSignal      = "signal"
	FailureClassStartFailed = "start_failed"
)

type CheckResult struct {
	Passed             bool
	ExitCode           int
	FailureClass       string
	Signal             string
	Duration           time.Duration
	LogPath            string
	Truncated          bool
	FailureTail        []string
	EvidenceCleanupErr error
}

func RunCheck(opts CheckOptions) (CheckResult, error) {
	if len(opts.Command) == 0 {
		return CheckResult{}, fmt.Errorf("check: command is required")
	}
	if err := os.MkdirAll(opts.EvidenceDir, 0o700); err != nil {
		return CheckResult{}, fmt.Errorf("create evidence directory: %w", err)
	}
	logFile, err := os.CreateTemp(opts.EvidenceDir, "check-*.log")
	if err != nil {
		return CheckResult{}, fmt.Errorf("create evidence log: %w", err)
	}
	logPath := logFile.Name()
	defer func() { _ = logFile.Close() }()

	output := outputlimit.New(MaxEvidenceLogBytes)
	child := execCommand(opts.Command[0], opts.Command[1:]...)
	child.Stdin = opts.Stdin
	child.Stdout = output
	child.Stderr = output
	child.Env = opts.Env
	child.ExtraFiles = opts.ExtraFiles
	if opts.Dir != "" {
		child.Dir = opts.Dir
	}
	runner := opts.Run
	if runner == nil {
		runner = (*exec.Cmd).Run
	}

	started := time.Now()
	runErr := runner(child)
	duration := time.Since(started).Round(time.Millisecond)
	if _, err := logFile.Write(output.Bytes()); err != nil {
		return CheckResult{}, fmt.Errorf("write evidence log: %w", err)
	}
	if err := logFile.Close(); err != nil {
		return CheckResult{}, fmt.Errorf("close evidence log: %w", err)
	}

	if runErr == nil && !opts.KeepSuccessLog {
		_ = os.Remove(logPath)
		return CheckResult{Passed: true, Duration: duration}, nil
	}

	cleanupErr := pruneEvidence(opts.EvidenceDir, logPath)
	if runErr == nil {
		return CheckResult{Passed: true, Duration: duration, LogPath: logPath, EvidenceCleanupErr: cleanupErr}, nil
	}
	failureClass, signalName := classifyFailure(runErr)
	return CheckResult{
		Passed:             false,
		ExitCode:           commandExitCode(runErr),
		FailureClass:       failureClass,
		Signal:             signalName,
		Duration:           duration,
		LogPath:            logPath,
		Truncated:          output.Truncated(),
		FailureTail:        output.LastLines(opts.TailLines, maxFailureTailBytes),
		EvidenceCleanupErr: cleanupErr,
	}, nil
}

func classifyFailure(err error) (failureClass string, signalName string) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return FailureClassStartFailed, ""
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return FailureClassSignal, status.Signal().String()
	}
	return FailureClassExit, ""
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
