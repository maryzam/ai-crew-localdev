//go:build integration

package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/runtelemetry"
)

const (
	captureDirEnv       = "AI_CREW_TELEMETRY_CAPTURE_DIR"
	claudeVersionsEnv   = "AI_CREW_CLAUDE_VERSIONS"
	codexVersionsEnv    = "AI_CREW_CODEX_VERSIONS"
	pinnedClaudeVersion = "2.1.302"
	pinnedCodexVersion  = "0.163.0"
	agentHome           = "/agent-home"
	workspace           = "/workspaces/spike"
	divertedEndpoint    = "http://127.0.0.1:9"
	forgedRunID         = "forged-run"
	statsInterval       = 200 * time.Millisecond
	counterSettleBudget = 15 * time.Second
)

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
	collector   runtelemetry.Collector
	forwarder   *runtelemetry.Forwarder
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

func token(t *testing.T, prefix string) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return prefix + hex.EncodeToString(b)
}

func shortTempDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
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
	file.WriteString(`ENTRYPOINT ["` + runtelemetry.OTLPForwarderPath + `", "/usr/local/bin/forward-model"]` + "\n")
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

func newRoot(t *testing.T) *evidence.Root {
	t.Helper()
	root, err := evidence.OpenRoot(filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func startCollector(t *testing.T, root *evidence.Root) runtelemetry.Collector {
	t.Helper()
	requirePodman(t)
	c := runtelemetry.Collector{Name: token(t, "ai-crew-collector-test-"), RuntimeDir: shortTempDir(t, "nc"), Root: root}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return c
}

func newTelemetryRun(t *testing.T, cli cliImage, model func(sentinels, *modelLog) http.Handler, hold time.Duration, withCollector bool) *telemetryRun {
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
	run.collector = runtelemetry.Collector{Name: token(t, "ai-crew-collector-test-"), RuntimeDir: shortTempDir(t, "nc"), Root: run.root}
	if withCollector {
		run.collector = startCollector(t, run.root)
	}
	var err error
	run.forwarder, err = runtelemetry.NewForwarder(run.context, run.collector.Socket())
	if err != nil {
		t.Fatal(err)
	}
	statsCtx, cancel := context.WithCancel(context.Background())
	run.stopStats, run.statsDone = cancel, make(chan error, 1)
	go func() { run.statsDone <- run.forwarder.RecordStats(statsCtx, run.root, statsInterval) }()
	t.Cleanup(run.finishStats)
	serveUnix(t, filepath.Join(run.modelSocket, "model.sock"), model(run.sentinels, run.model))
	serveUnix(t, filepath.Join(run.runSocket, filepath.Base(runtelemetry.ContainerRunSocket)), run.forwarder)
	return run
}

func (r *telemetryRun) finishStats() {
	r.statsOnce.Do(func() {
		r.stopStats()
		if err := <-r.statsDone; err != nil {
			r.t.Errorf("record forwarder stats: %v", err)
		}
	})
}

func serveUnix(t *testing.T, path string, handler http.Handler) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
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
	args := []string{"run", "--rm", "--name", name, "--network", "none", "--userns", "keep-id",
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

func (r *telemetryRun) settle(t *testing.T) runtelemetry.Summary {
	t.Helper()
	deadline := time.Now().Add(counterSettleBudget)
	for {
		s, err := runtelemetry.Summarize(r.root, r.context.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if s.Records["logs"] > 0 && s.Collector["otelcol_receiver_accepted_log_records"] == int64(s.Records["logs"]) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("collector counters did not account for stored telemetry within %s: records %v counters %v", counterSettleBudget, s.Records, s.Collector)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (r *telemetryRun) stored(t *testing.T, label string, signals ...string) [][]byte {
	t.Helper()
	r.settle(t)
	if err := r.collector.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.finishStats()
	summary, err := runtelemetry.Summarize(r.root, r.context.RunID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s summary: bytes %d records %v forwarder %+v collector %v", label, summary.TelemetryBytes, summary.Records, summary.Forwarder, summary.Collector)
	if summary.Forwarder == nil || summary.Forwarder.Failed != 0 || summary.Forwarder.Rejected != 0 || summary.Forwarder.Forwarded == 0 {
		t.Errorf("%s forwarder evidence shows lost or refused telemetry: %+v", label, summary.Forwarder)
	}
	for name, value := range summary.Collector {
		if value != 0 && (strings.Contains(name, "refused") || strings.Contains(name, "failed")) {
			t.Errorf("%s collector reported %s = %d", label, name, value)
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
	groups := decodeLines(t, lines)
	inv := inventoryOf(groups)
	saveCapture(t, label, path, inv)
	for _, signal := range signals {
		if len(inv[signal]) == 0 {
			t.Errorf("%s stored no %s", label, signal)
		}
	}
	r.requireStampedContext(t, label, groups)
	return lines
}

func (r *telemetryRun) requireStampedContext(t *testing.T, label string, groups []signalGroup) {
	t.Helper()
	want := map[string]string{
		"ai.run.id":        r.context.RunID,
		"ai.task":          r.context.Task,
		"ai.agent":         string(r.cli.agent),
		"ai.agent.version": r.cli.version,
		"ai.project":       r.context.Project,
		"ai.repository":    r.context.Repository,
		"ai.branch":        r.context.Branch,
		"ai.image.digest":  r.context.ImageDigest,
		"ai.egress.mode":   r.context.EgressMode,
	}
	for _, g := range groups {
		for key, value := range want {
			if got, _ := attribute(g.group.Resource.Attributes, key); got != value {
				t.Errorf("%s %s resource %s = %q, want %q", label, g.signal, key, got, value)
			}
		}
	}
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
