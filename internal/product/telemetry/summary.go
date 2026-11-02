package telemetry

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"

	"github.com/maryzam/ai-crew-localdev/internal/product/evidence"
)

type usageEvent struct {
	sessionKeys        []string
	input              string
	output             string
	cacheRead          string
	cacheWrite         string
	reasoning          string
	cost               string
	completedKind      string
	inputIncludesCache bool
}

var usageEvents = map[string]usageEvent{
	"claude_code.api_request": {sessionKeys: []string{"session.id"}, input: "input_tokens", output: "output_tokens", cacheRead: "cache_read_tokens", cacheWrite: "cache_creation_tokens", cost: "cost_usd"},
	"codex.sse_event":         {sessionKeys: []string{"conversation.id", "session.id"}, input: "input_token_count", output: "output_token_count", cacheRead: "cached_token_count", reasoning: "reasoning_token_count", completedKind: "response.completed", inputIncludesCache: true},
}

type SessionUsage struct {
	Agent        string
	Session      string
	Model        string
	Requests     int64
	Input        *int64
	Output       *int64
	CacheRead    *int64
	CacheWrite   *int64
	Reasoning    *int64
	CostUSD      *float64
	PromptTokens int64
}

func (u SessionUsage) CacheHitShare() *float64 {
	if u.CacheRead == nil || u.PromptTokens == 0 {
		return nil
	}
	share := float64(*u.CacheRead) / float64(u.PromptTokens)
	return &share
}

type Summary struct {
	Run            string
	TelemetryBytes int64
	Records        map[string]int
	Malformed      int
	Receiver       *Stats
	Pruned         []evidence.PrunedTelemetry
	Usage          []SessionUsage
}

func Summarize(root *evidence.Root, run string) (Summary, error) {
	summary := Summary{Run: run, Records: map[string]int{}}
	path, err := root.TelemetryPath(run)
	if err != nil {
		return summary, err
	}
	if info, err := os.Stat(path); err == nil {
		summary.TelemetryBytes = info.Size()
	}
	sessions := map[[3]string]*SessionUsage{}
	summary.Malformed, err = evidence.EachLine(path, func(line []byte) error {
		return tally(line, summary.Records, sessions)
	})
	if err != nil {
		return summary, err
	}
	for _, session := range sessions {
		summary.Usage = append(summary.Usage, *session)
	}
	sort.Slice(summary.Usage, func(i, j int) bool {
		a, b := summary.Usage[i], summary.Usage[j]
		if a.Agent != b.Agent {
			return a.Agent < b.Agent
		}
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		return a.Model < b.Model
	})
	snapshots := map[string]Stats{}
	if _, err := root.RunEvents(run, func(event evidence.RunEvent) {
		if event.Event != ReceiverStatsEvent {
			return
		}
		var snapshot statsSnapshot
		if json.Unmarshal(event.Data, &snapshot) == nil {
			snapshots[snapshot.Instance] = snapshot.Stats
		}
	}); err != nil {
		return summary, err
	}
	if len(snapshots) > 0 {
		combined := Stats{}
		for _, stats := range snapshots {
			combined.Stored += stats.Stored
			combined.Rejected += stats.Rejected
			combined.Failed += stats.Failed
		}
		summary.Receiver = &combined
	}
	_, err = evidence.EachLine(root.PruningPath(), func(line []byte) error {
		var pruned evidence.PrunedTelemetry
		if err := json.Unmarshal(line, &pruned); err != nil {
			return err
		}
		if pruned.Run == run {
			summary.Pruned = append(summary.Pruned, pruned)
		}
		return nil
	})
	return summary, err
}

type otlpValue struct {
	StringValue *string
	IntValue    *json.Number
	DoubleValue *float64
}

func (v *otlpValue) UnmarshalJSON(data []byte) error {
	var fields struct {
		StringValue json.RawMessage `json:"stringValue"`
		IntValue    json.RawMessage `json:"intValue"`
		DoubleValue json.RawMessage `json:"doubleValue"`
	}
	if json.Unmarshal(data, &fields) != nil {
		return nil
	}
	if json.Unmarshal(fields.StringValue, &v.StringValue) != nil {
		v.StringValue = nil
	}
	if json.Unmarshal(fields.IntValue, &v.IntValue) != nil {
		v.IntValue = nil
	}
	if json.Unmarshal(fields.DoubleValue, &v.DoubleValue) != nil {
		v.DoubleValue = nil
	}
	return nil
}

type otlpAttribute struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpRecord struct {
	Body       otlpValue       `json:"body"`
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpLine struct {
	ResourceLogs []struct {
		Resource struct {
			Attributes []otlpAttribute `json:"attributes"`
		} `json:"resource"`
		ScopeLogs []struct {
			LogRecords []otlpRecord `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
	ResourceMetrics []struct {
		ScopeMetrics []struct {
			Metrics []json.RawMessage `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
	ResourceSpans []struct {
		ScopeSpans []struct {
			Spans []json.RawMessage `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

func tally(line []byte, records map[string]int, sessions map[[3]string]*SessionUsage) error {
	var payload otlpLine
	if err := json.Unmarshal(line, &payload); err != nil {
		return err
	}
	for _, group := range payload.ResourceLogs {
		agent := text(group.Resource.Attributes, "ai.agent")
		for _, scope := range group.ScopeLogs {
			records["logs"] += len(scope.LogRecords)
			for _, record := range scope.LogRecords {
				addUsage(agent, record, sessions)
			}
		}
	}
	for _, group := range payload.ResourceMetrics {
		for _, scope := range group.ScopeMetrics {
			records["metrics"] += len(scope.Metrics)
		}
	}
	for _, group := range payload.ResourceSpans {
		for _, scope := range group.ScopeSpans {
			records["spans"] += len(scope.Spans)
		}
	}
	return nil
}

func addUsage(agent string, record otlpRecord, sessions map[[3]string]*SessionUsage) {
	name := text(record.Attributes, "event.name")
	spec, ok := usageEvents[name]
	if !ok && record.Body.StringValue != nil {
		name = *record.Body.StringValue
		spec, ok = usageEvents[name]
	}
	if !ok || spec.completedKind != "" && text(record.Attributes, "event.kind") != spec.completedKind {
		return
	}
	session := ""
	for _, key := range spec.sessionKeys {
		if session = text(record.Attributes, key); session != "" {
			break
		}
	}
	model := text(record.Attributes, "model")
	key := [3]string{agent, session, model}
	usage := sessions[key]
	if usage == nil {
		usage = &SessionUsage{Agent: agent, Session: session, Model: model}
		sessions[key] = usage
	}
	usage.Requests++
	input := integer(record.Attributes, spec.input)
	cacheRead := integer(record.Attributes, spec.cacheRead)
	cacheWrite := integer(record.Attributes, spec.cacheWrite)
	add(&usage.Input, input)
	add(&usage.Output, integer(record.Attributes, spec.output))
	add(&usage.CacheRead, cacheRead)
	add(&usage.CacheWrite, cacheWrite)
	add(&usage.Reasoning, integer(record.Attributes, spec.reasoning))
	add(&usage.CostUSD, decimal(record.Attributes, spec.cost))
	for _, part := range []*int64{input, cacheRead, cacheWrite} {
		if part != nil && (!spec.inputIncludesCache || part == input) {
			usage.PromptTokens += *part
		}
	}
}

func add[T int64 | float64](total **T, value *T) {
	if value == nil {
		return
	}
	if *total == nil {
		*total = new(T)
	}
	**total += *value
}

func text(attributes []otlpAttribute, key string) string {
	for _, attribute := range attributes {
		if attribute.Key == key && attribute.Value.StringValue != nil {
			return *attribute.Value.StringValue
		}
	}
	return ""
}

func decimal(attributes []otlpAttribute, key string) *float64 {
	for _, attribute := range attributes {
		if attribute.Key != key || key == "" {
			continue
		}
		switch {
		case attribute.Value.DoubleValue != nil:
			return attribute.Value.DoubleValue
		case attribute.Value.IntValue != nil:
			if parsed, err := attribute.Value.IntValue.Float64(); err == nil {
				return &parsed
			}
		case attribute.Value.StringValue != nil:
			if parsed, err := strconv.ParseFloat(*attribute.Value.StringValue, 64); err == nil {
				return &parsed
			}
		}
	}
	return nil
}

func integer(attributes []otlpAttribute, key string) *int64 {
	value := decimal(attributes, key)
	if value == nil {
		return nil
	}
	rounded := int64(*value)
	return &rounded
}
