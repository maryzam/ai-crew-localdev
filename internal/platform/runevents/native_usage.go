package runevents

import "strconv"

type NativeUsageAccumulator struct {
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
	Recorded   bool
}

func (a *NativeUsageAccumulator) CollectOTLPLogs(payload any) {
	walkMaps(payload, func(item map[string]any) {
		attributes := decodeOTLPAttributes(item["attributes"])
		eventName := stringAttribute(attributes, "event.name")
		if eventName == "" {
			eventName = otlpString(item["body"])
		}
		usage, ok := nativeUsageFromEvent(eventName, attributes)
		if !ok {
			return
		}
		a.usage.Add(usage)
	})
}

func (a *NativeUsageAccumulator) Snapshot() NativeUsage {
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
	if next.Model != "" {
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

func nativeUsageFromEvent(eventName string, attributes map[string]any) (NativeUsage, bool) {
	var usage NativeUsage
	switch eventName {
	case "claude_code.api_request":
		usage.Input = intAttribute(attributes, "input_tokens")
		usage.Output = intAttribute(attributes, "output_tokens")
		usage.CacheRead = intAttribute(attributes, "cache_read_tokens")
		usage.CacheWrite = intAttribute(attributes, "cache_creation_tokens")
		usage.Total = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
		usage.CostUSD = floatAttribute(attributes, "cost_usd")
	case "codex.sse_event":
		if stringAttribute(attributes, "event.kind") != "response.completed" {
			return NativeUsage{}, false
		}
		usage.Input = intAttribute(attributes, "input_token_count")
		usage.Output = intAttribute(attributes, "output_token_count")
		usage.CacheRead = intAttribute(attributes, "cached_token_count")
		usage.Reasoning = intAttribute(attributes, "reasoning_token_count")
		usage.Total = usage.Input + usage.Output
	default:
		return NativeUsage{}, false
	}
	if usage.Total <= 0 {
		return NativeUsage{}, false
	}
	usage.Model = stringAttribute(attributes, "model")
	usage.Recorded = true
	return usage, true
}

func int64Value(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func walkMaps(value any, visit func(map[string]any)) {
	switch typed := value.(type) {
	case map[string]any:
		visit(typed)
		for _, child := range typed {
			walkMaps(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkMaps(child, visit)
		}
	}
}

func decodeOTLPAttributes(value any) map[string]any {
	result := make(map[string]any)
	items, _ := value.([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		key, _ := item["key"].(string)
		if key != "" {
			result[key] = otlpValue(item["value"])
		}
	}
	return result
}

func otlpValue(value any) any {
	item, _ := value.(map[string]any)
	for _, key := range []string{"stringValue", "intValue", "doubleValue", "boolValue"} {
		if found, ok := item[key]; ok {
			return found
		}
	}
	return nil
}

func otlpString(value any) string {
	if text, ok := otlpValue(value).(string); ok {
		return text
	}
	return ""
}

func stringAttribute(attributes map[string]any, key string) string {
	value, _ := attributes[key].(string)
	return value
}

func intAttribute(attributes map[string]any, key string) int64 {
	switch value := attributes[key].(type) {
	case string:
		result, _ := strconv.ParseInt(value, 10, 64)
		return result
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func floatAttribute(attributes map[string]any, key string) float64 {
	switch value := attributes[key].(type) {
	case string:
		result, _ := strconv.ParseFloat(value, 64)
		return result
	case float64:
		return value
	default:
		return 0
	}
}
