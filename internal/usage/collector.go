package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/outputlimit"
)

const collectorTimeout = time.Second

var (
	lookPath = exec.LookPath
	runTool  = runUsageTool
	now      = time.Now
)

type Totals struct {
	InputTokens         int64   `json:"inputTokens"`
	OutputTokens        int64   `json:"outputTokens"`
	CacheReadTokens     int64   `json:"cacheReadTokens"`
	CacheCreationTokens int64   `json:"cacheCreationTokens"`
	ReasoningTokens     int64   `json:"reasoningOutputTokens"`
	TotalTokens         int64   `json:"totalTokens"`
	TotalCost           float64 `json:"totalCost"`
	CostUSD             float64 `json:"costUSD"`
}

type report struct {
	Totals Totals `json:"totals"`
}

type Tracker struct {
	provider string
	toolPath string
	since    string
	before   Totals
	ready    bool
}

type Delta struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	TotalTokens      int64
	CostUSD          string
}

func Start(agentCommand []string) Tracker {
	provider := providerFor(agentCommand)
	if provider == "" {
		return Tracker{}
	}
	toolPath, err := lookPath("ccusage")
	if err != nil {
		return Tracker{}
	}
	since := now().Format("20060102")
	before, err := capture(toolPath, provider, since)
	if err != nil {
		return Tracker{}
	}
	return Tracker{provider: provider, toolPath: toolPath, since: since, before: before, ready: true}
}

func (t Tracker) Finish() (Delta, bool) {
	if !t.ready {
		return Delta{}, false
	}
	after, err := capture(t.toolPath, t.provider, t.since)
	if err != nil {
		return Delta{}, false
	}
	if !monotonic(t.before, after) {
		return Delta{}, false
	}
	delta := Delta{
		InputTokens:      after.InputTokens - t.before.InputTokens,
		OutputTokens:     after.OutputTokens - t.before.OutputTokens,
		CacheReadTokens:  after.CacheReadTokens - t.before.CacheReadTokens,
		CacheWriteTokens: after.CacheCreationTokens - t.before.CacheCreationTokens,
		ReasoningTokens:  after.ReasoningTokens - t.before.ReasoningTokens,
		TotalTokens:      after.TotalTokens - t.before.TotalTokens,
	}
	cost := costValue(after) - costValue(t.before)
	if cost > 0 {
		delta.CostUSD = strconv.FormatFloat(cost, 'f', 6, 64)
	}
	if delta.TotalTokens == 0 {
		delta.TotalTokens = delta.InputTokens + delta.OutputTokens + delta.CacheReadTokens + delta.CacheWriteTokens
	}
	return delta, delta.TotalTokens > 0
}

func providerFor(command []string) string {
	if len(command) == 0 {
		return ""
	}
	switch strings.TrimSuffix(filepath.Base(command[0]), ".exe") {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	default:
		return ""
	}
}

func capture(toolPath, provider, since string) (Totals, error) {
	data, err := runTool(toolPath, []string{provider, "daily", "--offline", "--json", "--since", since}, SafeEnv())
	if err != nil {
		return Totals{}, err
	}
	var value report
	if err := json.Unmarshal(data, &value); err != nil {
		return Totals{}, err
	}
	return value.Totals, nil
}

func runUsageTool(toolPath string, args, env []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), collectorTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, toolPath, args...)
	cmd.Env = env
	output := outputlimit.New(1024 * 1024)
	cmd.Stdout = output
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if output.Truncated() {
		return nil, fmt.Errorf("usage report exceeded 1 MiB")
	}
	return output.Bytes(), nil
}

func SafeEnv() []string {
	env := []string{"NO_COLOR=1"}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "TZ"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func monotonic(before, after Totals) bool {
	return after.InputTokens >= before.InputTokens &&
		after.OutputTokens >= before.OutputTokens &&
		after.CacheReadTokens >= before.CacheReadTokens &&
		after.CacheCreationTokens >= before.CacheCreationTokens &&
		after.ReasoningTokens >= before.ReasoningTokens &&
		after.TotalTokens >= before.TotalTokens &&
		costValue(after) >= costValue(before)
}

func costValue(value Totals) float64 {
	if value.CostUSD != 0 {
		return value.CostUSD
	}
	return value.TotalCost
}
