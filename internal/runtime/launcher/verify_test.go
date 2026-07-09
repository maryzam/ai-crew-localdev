package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
)

const verifyRetryCounterCmd = `n=0; [ -f "$CNT" ] && read n < "$CNT"; n=$((n+1)); printf %s "$n" > "$CNT"; [ "$n" -ge 3 ]`

func verifyTestEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())
	return append([]string{"PATH=/bin:/usr/bin"}, extra...)
}

func stubAgentCommand(t *testing.T, agentCalls *int, agentExit string) {
	t.Helper()
	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })
	execCommand = func(name string, args ...string) *exec.Cmd {
		if agentCalls != nil {
			*agentCalls++
		}
		return exec.Command(agentExit)
	}
}

func TestLaunchWithVerify_PassesOnFirstAttempt(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "cnt")
	env := verifyTestEnv(t, "CNT="+counter)
	var agentCalls int
	stubAgentCommand(t, &agentCalls, "/bin/true")

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    `printf 1 > "$CNT"`,
		MaxRetries:   2,
		RepoPath:     t.TempDir(),
	}, env, nil, "sess-test-pass", func() {}, disabledRecorderForTest(t), noopHomeFinalizer)

	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if agentCalls != 1 {
		t.Errorf("agent should run once, ran %d times", agentCalls)
	}
	if data, err := os.ReadFile(counter); err != nil || string(data) != "1" {
		t.Errorf("verify should run once, counter = %q (err %v)", data, err)
	}
}

func TestLaunchWithVerify_RetriesOnVerifyFailure(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "cnt")
	env := verifyTestEnv(t, "CNT="+counter)
	var agentCalls int
	stubAgentCommand(t, &agentCalls, "/bin/true")

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    verifyRetryCounterCmd,
		MaxRetries:   2,
		RepoPath:     t.TempDir(),
	}, env, nil, "sess-test-retry", func() {}, disabledRecorderForTest(t), noopHomeFinalizer)

	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if agentCalls != 3 {
		t.Errorf("agent should run 3 times (1 + 2 retries), ran %d times", agentCalls)
	}
	if data, err := os.ReadFile(counter); err != nil || string(data) != "3" {
		t.Errorf("verify should run 3 times, counter = %q (err %v)", data, err)
	}

	evidenceDir := filepath.Join(os.Getenv("AI_AGENT_CONFIG_DIR"), "evidence")
	entries, err := os.ReadDir(evidenceDir)
	if err != nil || len(entries) == 0 {
		t.Errorf("failed verify attempts must retain evidence logs in %s (entries %v, err %v)", evidenceDir, entries, err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".log") {
			t.Errorf("unexpected evidence entry %s", entry.Name())
		}
	}
}

func TestLaunchWithVerifyPassesBindFDToVerifyCommand(t *testing.T) {
	env := verifyTestEnv(t, "AI_AGENT_SESSION_BIND_FD=3")

	fd, err := CreateBindFD([]byte("bind-secret"))
	if err != nil {
		t.Fatalf("CreateBindFD: %v", err)
	}
	bindFile := os.NewFile(uintptr(fd), "ai-agent-session-bind")
	if bindFile == nil {
		t.Fatalf("os.NewFile(%d) returned nil", fd)
	}
	defer func() { _ = bindFile.Close() }()

	err = launchWithVerify("/bin/true", Options{
		AgentCommand: []string{"/bin/true"},
		VerifyCmd:    `test "$(cat "/proc/self/fd/$AI_AGENT_SESSION_BIND_FD")" = "bind-secret"`,
		RepoPath:     t.TempDir(),
	}, env, bindFile, "sess-test-verify-bind", func() {}, disabledRecorderForTest(t), noopHomeFinalizer)
	if err != nil {
		t.Fatalf("launchWithVerify: %v", err)
	}
}

func TestLaunchWithVerify_FailsAfterAllRetries(t *testing.T) {
	env := verifyTestEnv(t)
	var agentCalls int
	revoked := false
	stubAgentCommand(t, &agentCalls, "/bin/true")

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "false",
		MaxRetries:   1,
		RepoPath:     t.TempDir(),
	}, env, nil, "sess-test-fail", func() { revoked = true }, disabledRecorderForTest(t), noopHomeFinalizer)

	if err == nil {
		t.Fatal("expected error after all retries exhausted")
	}
	if agentCalls != 2 {
		t.Errorf("agent should run 2 times (1 + 1 retry), ran %d times", agentCalls)
	}
	if !revoked {
		t.Error("session should be revoked on final failure")
	}
}

func TestLaunchWithVerifyReturnsHomeFinalizeErrorAfterVerifyFailure(t *testing.T) {
	env := verifyTestEnv(t)
	var agentCalls int
	finalizeErr := errors.New("persist isolated home state: denied")
	stubAgentCommand(t, &agentCalls, "/bin/true")

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "false",
		MaxRetries:   0,
		RepoPath:     t.TempDir(),
	}, env, nil, "sess-test-home-finalize", func() {}, disabledRecorderForTest(t), func(*telemetry.Recorder) error {
		return finalizeErr
	})

	if err == nil {
		t.Fatal("expected error after verify failure")
	}
	if !strings.Contains(err.Error(), "verification failed after 1 attempt") {
		t.Fatalf("error = %v, want verification failure", err)
	}
	if !errors.Is(err, finalizeErr) {
		t.Fatalf("error = %v, want joined home finalize error", err)
	}
	if agentCalls != 1 {
		t.Fatalf("agent calls = %d, want 1", agentCalls)
	}
}

func TestLaunchWithVerify_AgentFailureStopsImmediately(t *testing.T) {
	env := verifyTestEnv(t)
	revoked := false
	stubAgentCommand(t, nil, "/bin/false")

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "true",
		MaxRetries:   5,
		RepoPath:     t.TempDir(),
	}, env, nil, "sess-test-agent-fail", func() { revoked = true }, disabledRecorderForTest(t), noopHomeFinalizer)

	if err == nil {
		t.Fatal("expected error when agent fails")
	}
	if !revoked {
		t.Error("session should be revoked when agent fails")
	}
}

func TestLaunchWithVerify_ZeroRetries(t *testing.T) {
	env := verifyTestEnv(t)
	var agentCalls int
	stubAgentCommand(t, &agentCalls, "/bin/true")

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "false",
		MaxRetries:   0,
		RepoPath:     t.TempDir(),
	}, env, nil, "sess-test-zero", func() {}, disabledRecorderForTest(t), noopHomeFinalizer)

	if err == nil {
		t.Fatal("expected error with 0 retries and failing verify")
	}
	if agentCalls != 1 {
		t.Errorf("agent should run exactly once with 0 retries, ran %d times", agentCalls)
	}
}

func TestLaunchWithVerify_CleansUpSessionFile(t *testing.T) {
	env := verifyTestEnv(t)

	sessID := "sess-cleanup-test"
	if err := SaveSessionInfo(SessionInfo{
		SessionID: sessID,
		AgentName: "test",
		Repo:      "o/r",
	}); err != nil {
		t.Fatalf("SaveSessionInfo: %v", err)
	}

	stubAgentCommand(t, nil, "/bin/true")

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "true",
		MaxRetries:   0,
		RepoPath:     t.TempDir(),
	}, env, nil, sessID, func() {}, disabledRecorderForTest(t), noopHomeFinalizer)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := LoadSessionInfo(sessID); err == nil {
		t.Error("session file should have been removed after cleanup")
	}
}

func TestLaunchWithVerify_InterruptedReturnsSignalExitCode(t *testing.T) {
	env := verifyTestEnv(t)
	revoked := false
	stubAgentCommand(t, nil, "/bin/true")

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "kill -TERM $$",
		MaxRetries:   3,
		RepoPath:     t.TempDir(),
	}, env, nil, "sess-test-interrupt", func() { revoked = true }, disabledRecorderForTest(t), noopHomeFinalizer)

	if err == nil {
		t.Fatal("expected error for interrupted verify")
	}
	var exitErr *AgentExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("interrupted verify must return an exit-coded error, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 143 {
		t.Fatalf("ExitCode() = %d, want 143 (128+SIGTERM)", exitErr.ExitCode())
	}
	if !revoked {
		t.Error("session should be revoked on interruption")
	}
}

func TestLaunchWithVerify_RunsContractsInOrderAndRetriesFirstFailure(t *testing.T) {
	logDir := t.TempDir()
	env := verifyTestEnv(t, "LOGDIR="+logDir, "CNT="+filepath.Join(logDir, "cnt"))
	var agentCalls int
	stubAgentCommand(t, &agentCalls, "/bin/true")

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		Contracts: []VerifyContract{
			{Name: "first", Command: `echo first >> "$LOGDIR/order"`, RetryAgent: true},
			{Name: "second", Command: `echo second >> "$LOGDIR/order"; ` + verifyRetryCounterCmd, RetryAgent: true},
			{Name: "third", Command: `echo third >> "$LOGDIR/order"`, RetryAgent: true},
		},
		MaxRetries: 2,
		RepoPath:   t.TempDir(),
	}, env, nil, "sess-contract-order", func() {}, disabledRecorderForTest(t), noopHomeFinalizer)

	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if agentCalls != 3 {
		t.Errorf("agent should run 3 times, ran %d", agentCalls)
	}
	order, readErr := os.ReadFile(filepath.Join(logDir, "order"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	want := "first\nsecond\nfirst\nsecond\nfirst\nsecond\nthird\n"
	if string(order) != want {
		t.Fatalf("contract order = %q, want %q (later contracts must not run past an earlier failure)", order, want)
	}
}

func TestLaunchWithVerify_RetryNeverFailsImmediately(t *testing.T) {
	env := verifyTestEnv(t)
	var agentCalls int
	stubAgentCommand(t, &agentCalls, "/bin/true")

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		Contracts: []VerifyContract{
			{Name: "no-retry", Command: "false", RetryAgent: false},
		},
		MaxRetries: 5,
		RepoPath:   t.TempDir(),
	}, env, nil, "sess-contract-never", func() {}, disabledRecorderForTest(t), noopHomeFinalizer)

	if err == nil || !strings.Contains(err.Error(), `"no-retry"`) {
		t.Fatalf("error = %v, want immediate failure naming the contract", err)
	}
	if agentCalls != 1 {
		t.Errorf("retry \"never\" must not relaunch the agent; agent ran %d times", agentCalls)
	}
}

func TestVerifyCmdOverridesContracts(t *testing.T) {
	opts := Options{
		VerifyCmd: "true",
		Contracts: []VerifyContract{{Name: "ignored", Command: "false"}},
	}
	contracts := opts.verifyContracts()
	if len(contracts) != 1 || contracts[0].Name != "verify-cmd" || !contracts[0].RetryAgent {
		t.Fatalf("verifyContracts = %+v, want the explicit verify-cmd override", contracts)
	}
}

func TestLaunchWithVerify_ContractsRunInManifestRoot(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "pwd.txt")
	env := verifyTestEnv(t, "OUT="+out)
	stubAgentCommand(t, nil, "/bin/true")

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		Contracts:    []VerifyContract{{Name: "where", Command: `pwd > "$OUT"`, RetryAgent: true}},
		ContractsDir: root,
		RepoPath:     subdir,
	}, env, nil, "sess-contract-dir", func() {}, disabledRecorderForTest(t), noopHomeFinalizer)
	if err != nil {
		t.Fatalf("launchWithVerify: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(data))
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(got); err != nil || resolved != want {
		t.Fatalf("contract ran in %q, want manifest root %q", got, root)
	}
}
