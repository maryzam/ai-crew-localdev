package blackbox

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
	"github.com/maryzam/ai-crew-localdev/internal/product/cli"
	"github.com/maryzam/ai-crew-localdev/internal/product/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/product/login"
	"github.com/maryzam/ai-crew-localdev/internal/product/paths"
	"github.com/maryzam/ai-crew-localdev/internal/product/telemetry"
	"github.com/maryzam/ai-crew-localdev/internal/product/wirecapture"
)

type unusedLogin struct{}

func (unusedLogin) Login(context.Context, string) (login.Credential, error) {
	return login.Credential{}, nil
}

func isolateHome(t *testing.T) *evidence.Root {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := paths.Evidence()
	if err != nil {
		t.Fatal(err)
	}
	root, err := evidence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runAI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := cli.NewRoot(agents.Default(), unusedLogin{}, &out, &out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestCaptureIsOnByDefaultAndOneCommandTurnsItOff(t *testing.T) {
	isolateHome(t)
	settings, err := wirecapture.LoadSettings()
	if err != nil || !settings.Enabled {
		t.Fatalf("default settings %+v, %v", settings, err)
	}
	if out, err := runAI(t, "capture", "status"); err != nil || !strings.Contains(out, "on") {
		t.Fatalf("status %q, %v", out, err)
	}
	if out, err := runAI(t, "capture", "off"); err != nil || !strings.Contains(out, "off") {
		t.Fatalf("off %q, %v", out, err)
	}
	if settings, err := wirecapture.LoadSettings(); err != nil || settings.Enabled {
		t.Fatalf("settings after off %+v, %v", settings, err)
	}
	if _, err := runAI(t, "capture", "on"); err != nil {
		t.Fatal(err)
	}
	if settings, err := wirecapture.LoadSettings(); err != nil || !settings.Enabled {
		t.Fatalf("settings after on %+v, %v", settings, err)
	}
}

func TestUnreadableCaptureSettingsAreRefusedRatherThanGuessed(t *testing.T) {
	isolateHome(t)
	if _, err := runAI(t, "capture", "off"); err != nil {
		t.Fatal(err)
	}
	config, err := paths.Config()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "wire-capture.json")
	for name, content := range map[string]string{"malformed": "{", "missing value": "{}", "unknown key": `{"enabled":true,"other":1}`} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := wirecapture.LoadSettings(); err == nil {
			t.Errorf("%s settings were accepted", name)
		}
	}
	if err := os.WriteFile(path, []byte(`{"enabled":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wirecapture.LoadSettings(); err == nil {
		t.Error("group-readable settings were accepted")
	}
}

func usageRecord(name string, attributes ...string) string {
	return `{"timeUnixNano":"1","body":{"stringValue":"` + name + `"},"attributes":[` + strings.Join(attributes, ",") + `]}`
}

func intAttribute(key, value string) string {
	return `{"key":"` + key + `","value":{"intValue":"` + value + `"}}`
}

func stringAttribute(key, value string) string {
	return `{"key":"` + key + `","value":{"stringValue":"` + value + `"}}`
}

func TestLogsShowUsageCacheCostAndLossWithoutInventingValues(t *testing.T) {
	root := isolateHome(t)
	const run = "run-usage"
	c := hostContext(run)
	r := startReceiver(t, root, c)
	claude := usageRecord("claude_code.api_request", stringAttribute("session.id", "claude-session"), stringAttribute("model", "claude-m"), intAttribute("input_tokens", "100"), intAttribute("output_tokens", "20"), intAttribute("cache_read_tokens", "30"), intAttribute("cache_creation_tokens", "5"), `{"key":"cost_usd","value":{"doubleValue":0.25}}`)
	codex := usageRecord("codex.sse_event", stringAttribute("conversation.id", "codex-session"), stringAttribute("model", "gpt-m"), stringAttribute("event.kind", "response.completed"), intAttribute("input_token_count", "80"), intAttribute("output_token_count", "20"), intAttribute("cached_token_count", "30"), intAttribute("reasoning_token_count", "10"))
	ignored := usageRecord("codex.sse_event", stringAttribute("event.kind", "response.created"), intAttribute("input_token_count", "999"))
	for path, record := range map[string]string{"/otlp/claude/v1/logs": claude, "/otlp/codex/v1/logs": codex + "," + ignored} {
		body := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[` + record + `]}]}]}`
		if status := r.post(t, path, "application/json", strings.NewReader(body), nil); status != http.StatusOK {
			t.Fatalf("%s status %d", path, status)
		}
	}
	r.post(t, "/otlp/gemini/v1/logs", "application/json", strings.NewReader("{}"), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.receiver.RecordStats(ctx, 1); err != nil {
		t.Fatal(err)
	}
	summary, err := telemetry.Summarize(root, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Usage) != 2 {
		t.Fatalf("usage %+v", summary.Usage)
	}
	claudeUsage, codexUsage := summary.Usage[0], summary.Usage[1]
	if claudeUsage.Requests != 1 || *claudeUsage.CostUSD != 0.25 || *claudeUsage.CacheWrite != 5 || claudeUsage.Reasoning != nil {
		t.Errorf("claude usage %+v", claudeUsage)
	}
	if hit := *claudeUsage.CacheHitShare(); hit < 0.222 || hit > 0.223 {
		t.Errorf("claude cache hit share %f, want 30/135", hit)
	}
	if codexUsage.Requests != 1 || codexUsage.CostUSD != nil || codexUsage.CacheWrite != nil || *codexUsage.Reasoning != 10 {
		t.Errorf("codex usage %+v", codexUsage)
	}
	if hit := *codexUsage.CacheHitShare(); hit != 0.375 {
		t.Errorf("codex cache hit share %f, want 30/80", hit)
	}
	out, err := runAI(t, "logs")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Run " + run, "2 stored, 1 rejected, 0 failed", "claude-session", "codex-session", "22.2%", "37.5%", "0.2500", "Wire capture: not recorded"} {
		if !strings.Contains(out, want) {
			t.Errorf("ai logs does not show %q:\n%s", want, out)
		}
	}
	if _, err := runAI(t, "logs", "missing-run"); err == nil {
		t.Error("ai logs accepted a run with no evidence")
	}
}

func TestLogsNeutraliseAgentControlledTextAndKeepTheRestOfABatch(t *testing.T) {
	root := isolateHome(t)
	r := startReceiver(t, root, hostContext("run-hostile"))
	hostile := usageRecord("claude_code.api_request", `{"key":"session.id","value":{"stringValue":"s\u001b]52;c;Zm9v\u0007\nReceiver: forged"}}`, stringAttribute("model", "m"), intAttribute("input_tokens", "7"), `{"key":"cost_usd","value":{"doubleValue":"NaN"}}`)
	body := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[` + hostile + `]}]}]}`
	if status := r.post(t, "/otlp/claude/v1/logs", "application/json", strings.NewReader(body), nil); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	out, err := runAI(t, "logs", "run-hostile")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "\x1b\x07") || strings.Contains(out, "\nReceiver: forged") {
		t.Fatalf("agent-controlled text reached the terminal: %q", out)
	}
	if strings.Contains(out, "unreadable") || !strings.Contains(out, "1 logs") {
		t.Fatalf("one odd attribute discarded the batch:\n%s", out)
	}
}

func TestExportReportsOmittedRecordsAndMissingChunks(t *testing.T) {
	root := isolateHome(t)
	const run = "run-damaged"
	w := startWireOn(t, root, run, wirecapture.DefaultLimits, echoUpstream)
	w.post(t, []byte(`{"a":1}`))
	if err := w.recorder.Close(); err != nil {
		t.Fatal(err)
	}
	dir, err := root.RunDir(run)
	if err != nil {
		t.Fatal(err)
	}
	wire := filepath.Join(dir, evidence.WireDir)
	file, err := os.OpenFile(filepath.Join(wire, "exchanges.jsonl"), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{torn\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	out, err := runAI(t, "capture", "export", run)
	if err == nil || !strings.Contains(err.Error(), "1 omitted") || !strings.Contains(out, `"agent":"claude"`) {
		t.Fatalf("export %q, %v", out, err)
	}
	if err := os.Remove(filepath.Join(wire, "chunks.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := runAI(t, "capture", "export", run); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("export without chunks returned %v", err)
	}
}

func TestExportPrintsCapturedTrafficAndLogsReportIt(t *testing.T) {
	root := isolateHome(t)
	const run = "run-export"
	recorder, err := wirecapture.Open(root, run, wirecapture.Settings{Enabled: true}, wirecapture.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte{0xff, 0xfe, 0x00, 0x01}
	front := httptest.NewServer(recorder.Middleware("claude", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write(binary)
	})))
	t.Cleanup(front.Close)
	response, err := http.Post(front.URL+"/v1/messages", "application/json", strings.NewReader(`{"prompt":"<hello> & more"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := runAI(t, "capture", "export", run)
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Agent       string `json:"agent"`
		RequestBody struct {
			Text string `json:"text"`
		} `json:"request_body"`
		ResponseBody struct {
			Base64 string `json:"base64"`
		} `json:"response_body"`
	}
	if err := json.Unmarshal([]byte(out), &exported); err != nil {
		t.Fatalf("export %q: %v", out, err)
	}
	if exported.Agent != "claude" || exported.RequestBody.Text != `{"prompt":"<hello> & more"}` || exported.ResponseBody.Base64 != "//4AAQ==" {
		t.Fatalf("export %+v", exported)
	}
	logs, err := runAI(t, "logs", run)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs, "Wire capture: on; 1 exchanges, 0 incomplete, 0 failed") {
		t.Fatalf("ai logs:\n%s", logs)
	}
}
