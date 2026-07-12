package runevents

import (
	"encoding/json"
	"testing"
)

func TestNativeUsageAccumulatorCollectsClaudeUsage(t *testing.T) {
	var payload any
	mustUnmarshalNativeUsage(t, `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"claude_code.api_request"},"attributes":[{"key":"model","value":{"stringValue":"claude-sonnet-test"}},{"key":"input_tokens","value":{"intValue":"100"}},{"key":"output_tokens","value":{"intValue":"20"}},{"key":"cache_read_tokens","value":{"intValue":"30"}},{"key":"cache_creation_tokens","value":{"intValue":"5"}},{"key":"cost_usd","value":{"doubleValue":0.25}}]}]}]}]}`, &payload)

	var accumulator NativeUsageAccumulator
	accumulator.CollectOTLPLogs(payload)
	usage := accumulator.Snapshot()
	if !usage.Recorded || usage.Input != 100 || usage.Output != 20 || usage.CacheRead != 30 || usage.CacheWrite != 5 || usage.Total != 155 || usage.Model != "claude-sonnet-test" {
		t.Fatalf("usage = %#v", usage)
	}
	runUsage := usage.RunUsage()
	if runUsage.TotalTokens == nil || *runUsage.TotalTokens != 155 || runUsage.CostAmount == nil || *runUsage.CostAmount != "0.250000" || runUsage.CostCurrency != "USD" {
		t.Fatalf("run usage = %#v", runUsage)
	}
}

func TestNativeUsageAccumulatorCollectsCodexUsage(t *testing.T) {
	var payload any
	mustUnmarshalNativeUsage(t, `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"codex.sse_event"},"attributes":[{"key":"event.kind","value":{"stringValue":"response.completed"}},{"key":"model","value":{"stringValue":"gpt-test"}},{"key":"input_token_count","value":{"intValue":"80"}},{"key":"output_token_count","value":{"intValue":"20"}},{"key":"cached_token_count","value":{"intValue":"30"}},{"key":"reasoning_token_count","value":{"intValue":"10"}}]}]}]}]}`, &payload)

	var accumulator NativeUsageAccumulator
	accumulator.CollectOTLPLogs(payload)
	usage := accumulator.Snapshot()
	if !usage.Recorded || usage.Input != 80 || usage.Output != 20 || usage.CacheRead != 30 || usage.Reasoning != 10 || usage.Total != 100 || usage.Model != "gpt-test" {
		t.Fatalf("usage = %#v", usage)
	}
	runUsage := usage.RunUsage()
	if runUsage.TotalTokens == nil || *runUsage.TotalTokens != 100 || runUsage.CostAmount != nil || runUsage.Source != "native_otel" || runUsage.Scope != "run" || runUsage.Confidence != "provider_reported" {
		t.Fatalf("run usage = %#v", runUsage)
	}
}

func TestNativeUsageAccumulatorIgnoresIncompleteAndZeroUsage(t *testing.T) {
	var payload any
	mustUnmarshalNativeUsage(t, `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"codex.sse_event"},"attributes":[{"key":"event.kind","value":{"stringValue":"response.started"}},{"key":"input_token_count","value":{"intValue":"80"}}]},{"body":{"stringValue":"claude_code.api_request"},"attributes":[{"key":"input_tokens","value":{"intValue":"0"}}]}]}]}]}`, &payload)

	var accumulator NativeUsageAccumulator
	accumulator.CollectOTLPLogs(payload)
	if usage := accumulator.Snapshot(); usage.Recorded {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestNativeUsageAccumulatorAggregatesMultipleEvents(t *testing.T) {
	var payload any
	mustUnmarshalNativeUsage(t, `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"claude_code.api_request"},"attributes":[{"key":"model","value":{"stringValue":"first-model"}},{"key":"input_tokens","value":{"intValue":"10"}},{"key":"output_tokens","value":{"intValue":"5"}}]},{"body":{"stringValue":"claude_code.api_request"},"attributes":[{"key":"model","value":{"stringValue":"second-model"}},{"key":"input_tokens","value":{"intValue":"7"}},{"key":"output_tokens","value":{"intValue":"3"}},{"key":"cost_usd","value":{"doubleValue":0.1}}]}]}]}]}`, &payload)

	var accumulator NativeUsageAccumulator
	accumulator.CollectOTLPLogs(payload)
	usage := accumulator.Snapshot()
	if !usage.Recorded || usage.Input != 17 || usage.Output != 8 || usage.Total != 25 || usage.Model != "second-model" || usage.CostUSD != 0.1 {
		t.Fatalf("usage = %#v", usage)
	}
}

func mustUnmarshalNativeUsage(t *testing.T, data string, value *any) {
	t.Helper()
	if err := json.Unmarshal([]byte(data), value); err != nil {
		t.Fatal(err)
	}
}
