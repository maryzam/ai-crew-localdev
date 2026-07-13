package launcher

import (
	"fmt"
	"io"
	"math"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/control/plan"
	"github.com/maryzam/ai-crew-localdev/internal/platform/runevents"
	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
)

const usdMicros = 1000000

type budgetMonitor struct {
	mu       sync.Mutex
	recorder *telemetry.Recorder
	out      io.Writer
	states   []budgetState
	command  *exec.Cmd
	done     chan struct{}
	stop     *budgetStop
	killArm  bool
}

type budgetState struct {
	budget   plan.Budget
	observed int64
	warned   bool
	stopped  bool
}

type budgetStop struct {
	budget    plan.Budget
	observed  int64
	threshold int64
}

type BudgetExceededError struct {
	budget    string
	metric    plan.BudgetMetric
	observed  int64
	threshold int64
	code      int
	err       error
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("budget %q exceeded (%s=%d, threshold=%d)", e.budget, e.metric, e.observed, e.threshold)
}

func (e *BudgetExceededError) Unwrap() error {
	return e.err
}

func (e *BudgetExceededError) ExitCode() int {
	return e.code
}

func newBudgetMonitor(budgets []plan.Budget, recorder *telemetry.Recorder, out io.Writer) *budgetMonitor {
	if out == nil {
		out = io.Discard
	}
	states := make([]budgetState, 0, len(budgets))
	for _, budget := range budgets {
		if budget.MeasurementSource != plan.BudgetMeasurementSourceNativeOTEL {
			continue
		}
		states = append(states, budgetState{budget: budget})
	}
	return &budgetMonitor{recorder: recorder, out: out, states: states}
}

func (m *budgetMonitor) HasHardStop() bool {
	if m == nil {
		return false
	}
	for _, state := range m.states {
		if state.budget.StopPolicy == plan.BudgetStopPolicyStopRun {
			return true
		}
	}
	return false
}

func (m *budgetMonitor) ObserveNativeUsage(usage runevents.NativeUsage) {
	if m == nil || !usage.Recorded {
		return
	}
	observations := nativeBudgetObservations(usage)
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.states {
		value := observations[m.states[i].budget.Metric]
		if value <= 0 {
			continue
		}
		m.states[i].observed += value
		m.checkThresholdsLocked(&m.states[i])
	}
}

func nativeBudgetObservations(usage runevents.NativeUsage) map[plan.BudgetMetric]int64 {
	observations := map[plan.BudgetMetric]int64{
		plan.BudgetMetricTokens: usage.Total,
	}
	if usage.CostUSD > 0 {
		observations[plan.BudgetMetricCost] = int64(math.Round(usage.CostUSD * usdMicros))
	}
	return observations
}

func (m *budgetMonitor) checkThresholdsLocked(state *budgetState) {
	budget := state.budget
	if budget.WarnAt > 0 && !state.warned && state.observed >= budget.WarnAt {
		state.warned = true
		m.recordThresholdLocked(budget, "warn", state.observed, budget.WarnAt)
	}
	if budget.StopPolicy != plan.BudgetStopPolicyStopRun || budget.StopAt <= 0 || state.stopped || state.observed < budget.StopAt {
		return
	}
	state.stopped = true
	m.stop = &budgetStop{budget: budget, observed: state.observed, threshold: budget.StopAt}
	m.recordThresholdLocked(budget, "stop", state.observed, budget.StopAt)
	m.signalStopLocked()
}

func (m *budgetMonitor) recordThresholdLocked(budget plan.Budget, outcome string, observed int64, threshold int64) {
	if m.recorder != nil {
		m.recorder.RecordBudgetThreshold(budget.Name, string(budget.Metric), string(budget.MeasurementSource), string(budget.StopPolicy), outcome, observed, threshold)
	}
	if outcome == "stop" && m.recorder != nil {
		m.recorder.SetDiagnostic("budget_exceeded", fmt.Sprintf("budget %q exceeded", budget.Name))
	}
	if m.out != nil {
		prefix := "warning"
		if outcome == "stop" {
			prefix = "stopping"
		}
		_, _ = fmt.Fprintf(m.out, "%s: budget %q %s threshold crossed (%s=%d, threshold=%d)\n", prefix, budget.Name, outcome, budget.Metric, observed, threshold)
	}
}

func (m *budgetMonitor) Attach(command *exec.Cmd) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.command = command
	m.done = make(chan struct{})
	m.killArm = false
	if m.stop != nil {
		m.signalStopLocked()
	}
}

func (m *budgetMonitor) Detach(command *exec.Cmd) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.command == command {
		if m.done != nil {
			close(m.done)
		}
		m.command = nil
		m.done = nil
	}
}

func (m *budgetMonitor) signalStopLocked() {
	if m.command == nil || m.command.Process == nil || m.killArm {
		return
	}
	process := m.command.Process
	done := m.done
	m.killArm = true
	_ = process.Signal(syscall.SIGTERM)
	go func() {
		select {
		case <-done:
			return
		case <-time.After(2 * time.Second):
			_ = process.Kill()
		}
	}()
}

func (m *budgetMonitor) StopError(processErr error) *BudgetExceededError {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	stop := m.stop
	m.mu.Unlock()
	if stop == nil {
		return nil
	}
	code := 1
	if processErr != nil {
		if found, ok := exitCode(processErr); ok && found != 0 {
			code = found
		}
	}
	return &BudgetExceededError{
		budget:    stop.budget.Name,
		metric:    stop.budget.Metric,
		observed:  stop.observed,
		threshold: stop.threshold,
		code:      code,
		err:       processErr,
	}
}

func budgetExitCode(err *BudgetExceededError) *int {
	if err == nil {
		return nil
	}
	code := err.ExitCode()
	return &code
}
