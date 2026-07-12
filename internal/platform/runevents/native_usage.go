package runevents

import (
	"strconv"
	"sync"
)

type NativeUsageAccumulator struct {
	mu    sync.Mutex
	usage NativeUsage
}

type NativeUsage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Reasoning  int64
	Total      int64
	CostUSD    float64
	Model      string
	ModelMixed bool
	Recorded   bool
}

func (a *NativeUsageAccumulator) Add(usage NativeUsage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.usage.Add(usage)
}

func (a *NativeUsageAccumulator) Snapshot() NativeUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.usage
}

func (u *NativeUsage) Add(next NativeUsage) {
	if !next.Recorded {
		return
	}
	u.Input += next.Input
	u.Output += next.Output
	u.CacheRead += next.CacheRead
	u.CacheWrite += next.CacheWrite
	u.Reasoning += next.Reasoning
	u.Total += next.Total
	u.CostUSD += next.CostUSD
	if next.ModelMixed || (u.Model != "" && next.Model != "" && u.Model != next.Model) {
		u.Model = ""
		u.ModelMixed = true
	} else if next.Model != "" {
		u.Model = next.Model
	}
	u.Recorded = true
}

func (u NativeUsage) RunUsage() Usage {
	result := Usage{
		Status:           "observed",
		InputTokens:      int64Value(u.Input),
		OutputTokens:     int64Value(u.Output),
		CacheReadTokens:  int64Value(u.CacheRead),
		CacheWriteTokens: int64Value(u.CacheWrite),
		ReasoningTokens:  int64Value(u.Reasoning),
		TotalTokens:      int64Value(u.Total),
		Source:           "native_otel",
		Scope:            "run",
		Precision:        "request",
		Confidence:       "provider_reported",
	}
	if u.CostUSD > 0 {
		cost := strconv.FormatFloat(u.CostUSD, 'f', 6, 64)
		result.CostAmount = &cost
		result.CostCurrency = "USD"
	}
	return result
}

func int64Value(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}
