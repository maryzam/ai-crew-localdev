package telemetry

import (
	"sync"
	"time"
)

type DeliveryBudgets struct {
	MaxPayloadBytes      int64
	MaxQueueDepth        int
	MaxExportLatency     time.Duration
	MaxLocalWriteLatency time.Duration
}

type DeliveryStats struct {
	Payloads                        uint64
	PayloadBytes                    int64
	MaxPayloadBytes                 int64
	DroppedEvents                   uint64
	RejectedEvents                  uint64
	QueueDepth                      int
	MaxQueueDepth                   int
	QueueSaturations                uint64
	Exports                         uint64
	ExportLatency                   time.Duration
	MaxExportLatency                time.Duration
	LocalWrites                     uint64
	LocalWriteLatency               time.Duration
	MaxLocalWriteLatency            time.Duration
	PayloadBudgetExceeded           uint64
	QueueBudgetExceeded             uint64
	ExportLatencyBudgetExceeded     uint64
	LocalWriteLatencyBudgetExceeded uint64
}

func DefaultDeliveryBudgets() DeliveryBudgets {
	return DeliveryBudgets{MaxPayloadBytes: 512 * 1024, MaxQueueDepth: otlpQueueSize, MaxExportLatency: 2 * time.Second, MaxLocalWriteLatency: 100 * time.Millisecond}
}

type deliveryMetrics struct {
	mu      sync.Mutex
	stats   DeliveryStats
	budgets DeliveryBudgets
	now     func() time.Time
}

func newDeliveryMetrics(budgets DeliveryBudgets) *deliveryMetrics {
	return &deliveryMetrics{budgets: budgets, now: time.Now}
}

func (m *deliveryMetrics) snapshot() DeliveryStats {
	if m == nil {
		return DeliveryStats{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

func (m *deliveryMetrics) budget() DeliveryBudgets {
	if m == nil {
		return DeliveryBudgets{}
	}
	return m.budgets
}

func (m *deliveryMetrics) started() time.Time {
	return m.now()
}

func (m *deliveryMetrics) payload(bytes int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats.Payloads++
	m.stats.PayloadBytes += int64(bytes)
	if int64(bytes) > m.stats.MaxPayloadBytes {
		m.stats.MaxPayloadBytes = int64(bytes)
	}
	if m.budgets.MaxPayloadBytes > 0 && int64(bytes) > m.budgets.MaxPayloadBytes {
		m.stats.PayloadBudgetExceeded++
	}
}

func (m *deliveryMetrics) queue(depth int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats.QueueDepth = depth
	if depth > m.stats.MaxQueueDepth {
		m.stats.MaxQueueDepth = depth
	}
	if m.budgets.MaxQueueDepth > 0 && depth > m.budgets.MaxQueueDepth {
		m.stats.QueueBudgetExceeded++
	}
}

func (m *deliveryMetrics) saturation(dropped uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats.QueueSaturations++
	m.stats.QueueBudgetExceeded++
	m.stats.DroppedEvents += dropped
}

func (m *deliveryMetrics) dropped(events uint64) {
	m.mu.Lock()
	m.stats.DroppedEvents += events
	m.mu.Unlock()
}

func (m *deliveryMetrics) rejected(events uint64) {
	m.mu.Lock()
	m.stats.RejectedEvents += events
	m.mu.Unlock()
}

func (m *deliveryMetrics) exported(start time.Time) {
	elapsed := m.now().Sub(start)
	if elapsed < 0 {
		elapsed = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats.Exports++
	m.stats.ExportLatency += elapsed
	if elapsed > m.stats.MaxExportLatency {
		m.stats.MaxExportLatency = elapsed
	}
	if m.budgets.MaxExportLatency > 0 && elapsed > m.budgets.MaxExportLatency {
		m.stats.ExportLatencyBudgetExceeded++
	}
}

func (m *deliveryMetrics) wroteLocal(start time.Time) {
	elapsed := m.now().Sub(start)
	if elapsed < 0 {
		elapsed = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats.LocalWrites++
	m.stats.LocalWriteLatency += elapsed
	if elapsed > m.stats.MaxLocalWriteLatency {
		m.stats.MaxLocalWriteLatency = elapsed
	}
	if m.budgets.MaxLocalWriteLatency > 0 && elapsed > m.budgets.MaxLocalWriteLatency {
		m.stats.LocalWriteLatencyBudgetExceeded++
	}
}
