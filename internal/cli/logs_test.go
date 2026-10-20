package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtelemetry"
)

type failingView struct{}

func (failingView) Project(context.Context, []byte) error {
	return context.DeadlineExceeded
}

func TestLogsDoesNotCreateMissingEvidenceRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	t.Setenv(paths.EnvEvidenceDir, path)
	command := newLogsCommand()
	if err := command.Execute(); err == nil {
		t.Fatal("logs succeeded without evidence")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("logs created evidence root %s", path)
	}
}

func TestLogsReadsEvidenceWhenPhoenixIsUnavailable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evidence")
	t.Setenv(paths.EnvEvidenceDir, dir)
	root, err := evidence.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := runtelemetry.NewReceiver(runtelemetry.Context{RunID: "run-phoenix-down", Task: "test", Project: "project", ImageDigest: "sha256:test", EgressMode: "open", Agents: map[runtelemetry.Agent]string{runtelemetry.Codex: "test"}}, root, runtelemetry.WithView(failingView{}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/otlp/codex/v1/traces", strings.NewReader(`{"resourceSpans":[{"scopeSpans":[{"spans":[{}]}]}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	receiver.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("receiver status = %d", response.Code)
	}
	if err := receiver.Close(); err != nil {
		t.Fatal(err)
	}
	command := newLogsCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Run:", "run-phoenix-down", "spans records:", "1", "Projection failures:", "1"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("logs output %q missing %q", output.String(), expected)
		}
	}
}
