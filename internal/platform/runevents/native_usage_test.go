package runevents

import (
	"sync"
	"testing"
)

func TestNativeUsageAccumulatorAggregatesNeutralDeltas(t *testing.T) {
	var accumulator NativeUsageAccumulator
	accumulator.Add(NativeUsage{
		Input:      100,
		Output:     20,
		CacheRead:  30,
		CacheWrite: 5,
		Total:      155,
		CostUSD:    0.25,
		Model:      "claude-sonnet-test",
		Recorded:   true,
	})
	accumulator.Add(NativeUsage{
		Input:    7,
		Output:   3,
		Total:    10,
		CostUSD:  0.1,
		Model:    "claude-sonnet-test",
		Recorded: true,
	})

	usage := accumulator.Snapshot()
	if !usage.Recorded || usage.Input != 107 || usage.Output != 23 || usage.CacheRead != 30 || usage.CacheWrite != 5 || usage.Total != 165 || usage.Model != "claude-sonnet-test" || usage.ModelMixed {
		t.Fatalf("usage = %#v", usage)
	}
	runUsage := usage.RunUsage()
	if runUsage.TotalTokens == nil || *runUsage.TotalTokens != 165 || runUsage.CostAmount == nil || *runUsage.CostAmount != "0.350000" || runUsage.CostCurrency != "USD" {
		t.Fatalf("run usage = %#v", runUsage)
	}
}

func TestNativeUsageAccumulatorIgnoresUnrecordedDelta(t *testing.T) {
	var accumulator NativeUsageAccumulator
	accumulator.Add(NativeUsage{Input: 100, Output: 20, Total: 120})
	if usage := accumulator.Snapshot(); usage.Recorded {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestNativeUsageAccumulatorMarksMixedModels(t *testing.T) {
	var accumulator NativeUsageAccumulator
	accumulator.Add(NativeUsage{Input: 80, Output: 20, Total: 100, Model: "first-model", Recorded: true})
	accumulator.Add(NativeUsage{Input: 10, Output: 5, Total: 15, Model: "second-model", Recorded: true})

	usage := accumulator.Snapshot()
	if !usage.Recorded || !usage.ModelMixed || usage.Model != "" || usage.Total != 115 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestNativeUsageAccumulatorAllowsConcurrentWritersAndReaders(t *testing.T) {
	var accumulator NativeUsageAccumulator
	const writers = 32
	var group sync.WaitGroup
	for range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 50 {
				accumulator.Add(NativeUsage{Input: 1, Output: 2, Total: 3, Model: "shared-model", Recorded: true})
				_ = accumulator.Snapshot()
			}
		}()
	}
	group.Wait()

	usage := accumulator.Snapshot()
	if !usage.Recorded || usage.Input != writers*50 || usage.Output != writers*100 || usage.Total != writers*150 || usage.Model != "shared-model" || usage.ModelMixed {
		t.Fatalf("usage = %#v", usage)
	}
}
