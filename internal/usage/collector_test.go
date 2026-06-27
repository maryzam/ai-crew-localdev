package usage

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestTrackerRecordsAutomaticDeltaWithMinimalEnvironment(t *testing.T) {
	originalLookPath, originalRunTool, originalNow := lookPath, runTool, now
	t.Cleanup(func() { lookPath, runTool, now = originalLookPath, originalRunTool, originalNow })
	lookPath = func(name string) (string, error) {
		if name != "ccusage" {
			t.Fatalf("lookup = %q", name)
		}
		return "/tool/ccusage", nil
	}
	now = func() time.Time { return time.Date(2026, 6, 28, 0, 0, 0, 0, time.UTC) }
	outputs := [][]byte{
		[]byte(`{"totals":{"inputTokens":10,"outputTokens":5,"cacheReadTokens":3,"cacheCreationTokens":2,"reasoningOutputTokens":1,"totalTokens":21,"costUSD":0.1}}`),
		[]byte(`{"totals":{"inputTokens":30,"outputTokens":15,"cacheReadTokens":8,"cacheCreationTokens":4,"reasoningOutputTokens":3,"totalTokens":60,"costUSD":0.25}}`),
	}
	runTool = func(path string, args, env []string) ([]byte, error) {
		if path != "/tool/ccusage" {
			t.Fatalf("path = %q", path)
		}
		wantArgs := []string{"codex", "daily", "--offline", "--json", "--since", "20260628"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args = %#v", args)
		}
		for _, item := range env {
			if item == "GITHUB_TOKEN=secret" {
				t.Fatal("secret environment passed to usage adapter")
			}
		}
		result := outputs[0]
		outputs = outputs[1:]
		return result, nil
	}
	t.Setenv("GITHUB_TOKEN", "secret")

	tracker := Start([]string{"/usr/local/bin/codex"})
	delta, ok := tracker.Finish()
	if !ok {
		t.Fatal("expected usage delta")
	}
	if delta.InputTokens != 20 || delta.OutputTokens != 10 || delta.TotalTokens != 39 || delta.CostUSD != "0.150000" {
		t.Fatalf("delta = %#v", delta)
	}
}

func TestTrackerDisablesUnsupportedOrUnavailableAdapters(t *testing.T) {
	originalLookPath := lookPath
	t.Cleanup(func() { lookPath = originalLookPath })
	lookPath = func(string) (string, error) { return "", errors.New("missing") }

	if _, ok := Start([]string{"claude"}).Finish(); ok {
		t.Fatal("missing adapter should not report usage")
	}
	if _, ok := Start([]string{"other-agent"}).Finish(); ok {
		t.Fatal("unsupported agent should not report usage")
	}
}

func TestTrackerRejectsNonMonotonicTotals(t *testing.T) {
	originalRunTool := runTool
	t.Cleanup(func() { runTool = originalRunTool })
	runTool = func(string, []string, []string) ([]byte, error) {
		return []byte(`{"totals":{"totalTokens":4}}`), nil
	}
	tracker := Tracker{provider: "claude", toolPath: "/tool", since: "20260628", before: Totals{TotalTokens: 5}, ready: true}
	if _, ok := tracker.Finish(); ok {
		t.Fatal("non-monotonic totals should not be attributed")
	}
}
