package boundedqueue

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type Queue[T any] struct {
	items    chan entry[T]
	maxBytes int64
	deliver  func(context.Context, T)
	cancel   context.CancelFunc
	drained  chan struct{}
	mu       sync.Mutex
	bytes    int64
	closed   bool
	dropped  atomic.Uint64
}

type entry[T any] struct {
	value T
	size  int64
}

func Start[T any](maxItems int, maxBytes int64, deliver func(context.Context, T)) *Queue[T] {
	ctx, cancel := context.WithCancel(context.Background())
	q := &Queue[T]{items: make(chan entry[T], maxItems), maxBytes: maxBytes, deliver: deliver, cancel: cancel, drained: make(chan struct{})}
	go q.run(ctx)
	return q
}

func (q *Queue[T]) Offer(value T, size int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || size > q.maxBytes-q.bytes {
		return false
	}
	select {
	case q.items <- entry[T]{value: value, size: size}:
		q.bytes += size
		return true
	default:
		return false
	}
}

func (q *Queue[T]) Close(drainBudget time.Duration) uint64 {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.items)
	}
	q.mu.Unlock()
	timer := time.NewTimer(drainBudget)
	defer timer.Stop()
	select {
	case <-q.drained:
	case <-timer.C:
		q.cancel()
		<-q.drained
	}
	q.cancel()
	return q.dropped.Load()
}

func (q *Queue[T]) run(ctx context.Context) {
	defer close(q.drained)
	for item := range q.items {
		q.mu.Lock()
		q.bytes -= item.size
		q.mu.Unlock()
		if ctx.Err() != nil {
			q.dropped.Add(1)
			continue
		}
		q.deliver(ctx, item.value)
	}
}
