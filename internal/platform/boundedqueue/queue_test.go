package boundedqueue

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueueRefusesItemsBeyondItemAndByteBudgets(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	q := Start(2, 10, func(ctx context.Context, _ int) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
	})
	if !q.Offer(0, 1) {
		t.Fatal("first item refused")
	}
	<-started
	if !q.Offer(1, 6) || q.Offer(2, 5) || !q.Offer(3, 4) || q.Offer(4, 0) {
		t.Fatal("queue did not enforce its byte and item budgets")
	}
	close(release)
	if dropped := q.Close(time.Second); dropped != 0 {
		t.Fatalf("dropped = %d", dropped)
	}
	if q.Offer(5, 0) {
		t.Fatal("closed queue accepted an item")
	}
}

func TestQueueDrainsWithinBudgetAndReportsUndeliveredItems(t *testing.T) {
	var delivered, canceled atomic.Int32
	q := Start(8, 1<<20, func(ctx context.Context, blocking bool) {
		if !blocking {
			delivered.Add(1)
			return
		}
		<-ctx.Done()
		canceled.Add(1)
	})
	for _, blocking := range []bool{false, false, true, false, false} {
		if !q.Offer(blocking, 1) {
			t.Fatal("item refused")
		}
	}
	start := time.Now()
	dropped := q.Close(100 * time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("close exceeded its drain budget: %s", elapsed)
	}
	if delivered.Load() != 2 || canceled.Load() != 1 || dropped != 2 {
		t.Fatalf("delivered %d canceled %d dropped %d", delivered.Load(), canceled.Load(), dropped)
	}
	if again := q.Close(0); again != dropped {
		t.Fatalf("second close = %d", again)
	}
}
