package limits

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// Gate errors.
var (
	ErrQueueFull    = errors.New("queue full")
	ErrQueueTimeout = errors.New("queue wait timed out")
)

// Gate is the global admission gate (DECISIONS D4): a buffered-channel
// semaphore of maxInflight slots plus a bounded number of waiters. Blocked
// senders on a Go channel are woken in arrival order, which is fair enough
// here without a hand-off queue.
type Gate struct {
	slots     chan struct{}
	waiting   atomic.Int64
	queueSize int64
	timeout   time.Duration
}

// NewGate returns a gate with maxInflight (≥ 1) slots and room for queueSize
// waiters, each waiting at most queueTimeout.
func NewGate(maxInflight, queueSize int, queueTimeout time.Duration) *Gate {
	return &Gate{
		slots:     make(chan struct{}, max(maxInflight, 1)),
		queueSize: int64(max(queueSize, 0)),
		timeout:   queueTimeout,
	}
}

// Acquire takes a slot, waiting in the queue if needed. It fails fast with
// ErrQueueFull, after the queue timeout with ErrQueueTimeout, or with the
// context's error. On success it returns an idempotent release and the time
// spent waiting.
func (g *Gate) Acquire(ctx context.Context) (release func(), waited time.Duration, err error) {
	select {
	case g.slots <- struct{}{}:
		return g.releaser(), 0, nil
	default:
	}
	if g.waiting.Add(1) > g.queueSize {
		g.waiting.Add(-1)
		return nil, 0, ErrQueueFull
	}
	defer g.waiting.Add(-1)
	start := time.Now()
	timer := time.NewTimer(g.timeout)
	defer timer.Stop()
	select {
	case g.slots <- struct{}{}:
		return g.releaser(), time.Since(start), nil
	case <-timer.C:
		return nil, time.Since(start), ErrQueueTimeout
	case <-ctx.Done():
		return nil, time.Since(start), ctx.Err()
	}
}

func (g *Gate) releaser() func() {
	var done atomic.Bool
	return func() {
		if done.CompareAndSwap(false, true) {
			<-g.slots
		}
	}
}

// Stats returns the slots in use and the requests waiting.
func (g *Gate) Stats() (inflight, queued int) {
	return len(g.slots), int(g.waiting.Load())
}
