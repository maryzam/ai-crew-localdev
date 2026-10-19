//go:build integration

package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/runtelemetry"
)

const (
	claudeVersionsEnv   = "AI_CREW_CLAUDE_VERSIONS"
	codexVersionsEnv    = "AI_CREW_CODEX_VERSIONS"
	pinnedClaudeVersion = "2.1.302"
	pinnedCodexVersion  = "0.163.0"
	agentHome           = "/agent-home"
	workspace           = "/workspaces/spike"
	divertedEndpoint    = "http://127.0.0.1:9"
	forgedRunID         = "forged-run"
	statsInterval       = 200 * time.Millisecond
)

var agentUserNamespace = "keep-id:uid=" + strconv.Itoa(runtelemetry.ContainerAgentUID) + ",gid=" + strconv.Itoa(runtelemetry.ContainerAgentUID)

type cliImage struct {
	agent   runtelemetry.Agent
	version string
	image   string
	digest  string
}

type telemetryRun struct {
	t           *testing.T
	context     runtelemetry.Context
	cli         cliImage
	sentinels   sentinels
	runSocket   string
	modelSocket string
	home        string
	workspace   string
	root        *evidence.Root
	receiver    *runtelemetry.Receiver
	model       *modelLog
	stopStats   context.CancelFunc
	statsDone   chan error
	statsOnce   sync.Once
}

type cliRun struct {
	label     string
	env       []string
	home      map[string]string
	workspace map[string]string
	command   []string
}

func requirePodman(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not available")
	}
}

func versionsFrom(env, pinned string) []string {
	if v := strings.Fields(os.Getenv(env)); len(v) > 0 {
		return v
	}
	return []string{pinned}
}

func buildCLIImage(t *testing.T, agent runtelemetry.Agent, version string) cliImage {
	t.Helper()
	requirePodman(t)
	context := t.TempDir()
	base, err := os.ReadFile(filepath.Join("testdata", string(agent), "Containerfile"))
	if err != nil {
		t.Fatal(err)
	}
	var file bytes.Buffer
	file.Write(base)
	for _, env := range runtelemetry.ImageEnv() {
		file.WriteString("ENV " + env + "\n")
	}
	file.WriteString(`ENTRYPOINT ["/usr/local/bin/forward-model", "` + runtelemetry.OTLPForwarderPath + `"]` + "\n")
	if err := os.WriteFile(filepath.Join(context, "Containerfile"), file.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	forwarder, err := os.ReadFile(filepath.Join("testdata", "forward-model"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(context, "forward-model"), forwarder, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, f := range runtelemetry.ImageFiles() {
		target := filepath.Join(context, "image", path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, f.Content, f.Mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(target, f.Mode); err != nil {
			t.Fatal(err)
		}
	}
	arg := strings.ToUpper(string(agent)) + "_VERSION=" + version
	image := "localhost/ai-crew-telemetry-" + string(agent) + ":" + version
	if out, err := exec.Command("podman", "build", "-q", "--build-arg", arg, "-t", image, context).CombinedOutput(); err != nil {
		t.Fatalf("build %s %s image: %v\n%s", agent, version, err, out)
	}
	digest, err := exec.Command("podman", "image", "inspect", "--format", "{{.Id}}", image).Output()
	if err != nil {
		t.Fatal(err)
	}
	return cliImage{agent: agent, version: version, image: image, digest: "sha256:" + strings.TrimSpace(string(digest))}
}

func newTelemetryRun(t *testing.T, cli cliImage, model func(sentinels, *modelLog) http.Handler, hold time.Duration) *telemetryRun {
	t.Helper()
	run := &telemetryRun{
		t: t,
		context: runtelemetry.Context{
			RunID:       token(t, "run-"),
			Task:        "telemetry-check",
			Project:     "spike",
			Repository:  "example/spike",
			Branch:      "ai/telemetry-check/main",
			ImageDigest: cli.digest,
			EgressMode:  "none",
			Agents:      map[runtelemetry.Agent]string{cli.agent: cli.version},
		},
		cli:         cli,
		sentinels:   sentinels{Prompt: token(t, "promptsentinel"), Response: token(t, "responsesentinel"), ToolInput: token(t, "toolinsentinel"), ToolOutput: token(t, "toolout-")},
		runSocket:   shortTempDir(t, "nr"),
		modelSocket: shortTempDir(t, "nm"),
		home:        t.TempDir(),
		workspace:   t.TempDir(),
		root:        newRoot(t),
		model:       newModelLog(hold),
	}
	var err error
	run.receiver, err = runtelemetry.NewReceiver(run.context, run.root)
	if err != nil {
		t.Fatal(err)
	}
	statsCtx, cancel := context.WithCancel(context.Background())
	run.stopStats, run.statsDone = cancel, make(chan error, 1)
	go func() { run.statsDone <- run.receiver.RecordStats(statsCtx, statsInterval) }()
	t.Cleanup(run.finishStats)
	serveUnix(t, filepath.Join(run.modelSocket, "model.sock"), model(run.sentinels, run.model))
	serveUnix(t, filepath.Join(run.runSocket, filepath.Base(runtelemetry.ContainerRunSocket)), run.receiver)
	return run
}

func (r *telemetryRun) finishStats() {
	r.statsOnce.Do(func() {
		r.stopStats()
		if err := <-r.statsDone; err != nil {
			r.t.Errorf("record receiver stats: %v", err)
		}
	})
}

func writeFiles(t *testing.T, root string, files map[string]string, mode os.FileMode) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *telemetryRun) command(t *testing.T, c cliRun) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	writeFiles(t, r.home, c.home, 0o600)
	writeFiles(t, r.workspace, c.workspace, 0o644)
	name := "ai-crew-telemetry-" + strings.TrimPrefix(r.context.RunID, "run-")
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", "-t", "0", "--ignore", name).Run() })
	args := []string{"run", "--rm", "--name", name, "--network", "none", "--userns", agentUserNamespace, "--user", "0:0",
		"-v", r.runSocket + ":" + filepath.Dir(runtelemetry.ContainerRunSocket),
		"-v", r.modelSocket + ":/run/spike",
		"-v", r.home + ":" + agentHome, "-v", r.workspace + ":" + workspace, "-e", "HOME=" + agentHome}
	for _, e := range c.env {
		args = append(args, "-e", e)
	}
	args = append(append(args, r.cli.image), c.command...)
	cmd := exec.Command("podman", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	return cmd, &stdout, &stderr, name
}

func (r *telemetryRun) execute(t *testing.T, c cliRun) {
	t.Helper()
	cmd, stdout, stderr, _ := r.command(t, c)
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(4 * time.Minute):
		_ = cmd.Process.Kill()
		t.Fatalf("%s timed out", c.label)
	}
	paths, toolUses, finals := r.model.summary()
	t.Logf("%s model requests: %v", c.label, paths)
	if err != nil {
		t.Fatalf("%s failed: %v\nstderr:\n%s", c.label, err, r.redact(stderr.String()))
	}
	if toolUses != 1 || finals != 1 {
		t.Fatalf("%s did not complete the scripted tool turn: tool calls %d, final answers %d\nstderr:\n%s", c.label, toolUses, finals, r.redact(stderr.String()))
	}
	if !strings.Contains(stdout.String(), r.sentinels.Response) {
		t.Fatalf("%s did not print the scripted answer", c.label)
	}
}

func (r *telemetryRun) executeAndKill(t *testing.T, c cliRun, afterFinalRequest time.Duration) {
	t.Helper()
	cmd, _, stderr, name := r.command(t, c)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.model.finalSeen:
	case <-time.After(3 * time.Minute):
		_ = cmd.Process.Kill()
		t.Fatalf("%s never requested its final answer\nstderr:\n%s", c.label, r.redact(stderr.String()))
	}
	time.Sleep(afterFinalRequest)
	if out, err := exec.Command("podman", "kill", "--signal", "KILL", name).CombinedOutput(); err != nil {
		t.Fatalf("kill %s: %v\n%s", name, err, out)
	}
	_ = cmd.Wait()
}

func (r *telemetryRun) redact(s string) string {
	for _, v := range r.sentinels.byLabel() {
		s = strings.ReplaceAll(s, v, "<sentinel>")
	}
	return s
}

func (r *telemetryRun) stored(t *testing.T, label string, signals ...string) [][]byte {
	t.Helper()
	r.finishStats()
	summary, err := runtelemetry.Summarize(r.root, r.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s summary: bytes %d records %v receiver %+v", label, summary.TelemetryBytes, summary.Records, summary.Receiver)
	if summary.Receiver == nil || summary.Receiver.Failed != 0 || summary.Receiver.Rejected != 0 || summary.Receiver.Stored == 0 {
		t.Errorf("%s receiver evidence shows lost or refused telemetry: %+v", label, summary.Receiver)
	}
	for _, signal := range signals {
		if summary.Records[signal] == 0 {
			t.Errorf("%s stored no %s", label, signal)
		}
	}
	runs, err := os.ReadDir(filepath.Join(r.root.Path(), evidence.RunsDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range runs {
		if e.Name() != r.context.RunID {
			t.Errorf("%s stored telemetry under %s", label, e.Name())
		}
	}
	path, _ := r.root.TelemetryPath(r.context.RunID)
	lines := readLines(t, path)
	requireStampedContext(t, label, decodeResources(t, lines), stampedContext(r.context, r.cli.agent))
	return lines
}

func requireContent(t *testing.T, label string, lines [][]byte, s map[string]string, kinds ...string) {
	t.Helper()
	present := presentSentinels(lines, s)
	for _, kind := range kinds {
		if !present[kind] {
			t.Errorf("%s stored no %s content", label, kind)
		}
	}
}

func token(t *testing.T, prefix string) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return prefix + hex.EncodeToString(b)
}

func presentSentinels(lines [][]byte, sentinels map[string]string) map[string]bool {
	present := map[string]bool{}
	for label, sentinel := range sentinels {
		for _, line := range lines {
			if bytes.Contains(line, []byte(sentinel)) {
				present[label] = true
				break
			}
		}
	}
	return present
}
