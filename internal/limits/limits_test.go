package limits

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestRateLimiterBurstAndRetryAfter(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	l := NewRateLimiter(clk.now)
	for i := range 3 {
		if ok, _ := l.Allow(1, 6, 3); !ok {
			t.Fatalf("request %d refused inside burst", i)
		}
	}
	ok, wait := l.Allow(1, 6, 3)
	if ok || wait != 10*time.Second {
		t.Fatalf("4th: ok=%v wait=%s, want refused with 10s", ok, wait)
	}
	if ok, _ := l.Allow(2, 6, 3); !ok {
		t.Error("another key is affected")
	}
	clk.add(4 * time.Second)
	if _, wait := l.Allow(1, 6, 3); wait != 6*time.Second {
		t.Errorf("after 4s wait=%s, want 6s", wait)
	}
	clk.add(6 * time.Second)
	if ok, _ := l.Allow(1, 6, 3); !ok {
		t.Error("refused after refill")
	}
	for range 100 {
		if ok, _ := l.Allow(3, 0, 1); !ok {
			t.Fatal("rpm 0 must be unlimited")
		}
	}
}

func TestRateLimiterRetryAfterAtLeastOneSecond(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	l := NewRateLimiter(clk.now)
	l.Allow(1, 6000, 1)
	if ok, wait := l.Allow(1, 6000, 1); ok || wait < time.Second {
		t.Errorf("ok=%v wait=%s", ok, wait)
	}
}

func TestKeyInflight(t *testing.T) {
	k := NewKeyInflight()
	r1, ok := k.TryAcquire(1, 2)
	_, ok2 := k.TryAcquire(1, 2)
	if !ok || !ok2 {
		t.Fatal("refused under the limit")
	}
	if _, ok := k.TryAcquire(1, 2); ok {
		t.Fatal("third accepted")
	}
	r1()
	r1() // idempotent
	if _, ok := k.TryAcquire(1, 2); !ok {
		t.Fatal("refused after release")
	}
	if _, ok := k.TryAcquire(1, 2); ok {
		t.Fatal("double release freed two slots")
	}
	if _, ok := k.TryAcquire(9, 0); !ok {
		t.Fatal("limit 0 must be unlimited")
	}
}

type fakeUsage struct {
	used  int64
	since time.Time
	err   error
}

func (f *fakeUsage) TokensSince(_ context.Context, _ int64, since time.Time) (int64, error) {
	f.since = since
	return f.used, f.err
}

func TestQuota(t *testing.T) {
	now := time.Date(2026, 10, 14, 23, 59, 30, 0, time.UTC)
	u := &fakeUsage{used: 999}
	q := NewQuota(u, func() time.Time { return now })
	if _, err := q.Check(context.Background(), 1, 1000); err != nil {
		t.Errorf("under quota: %v", err)
	}
	if !u.since.Equal(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("window starts %s", u.since)
	}
	u.used = 1000
	wait, err := q.Check(context.Background(), 1, 1000)
	if !errors.Is(err, ErrQuotaExceeded) || wait != 30*time.Second {
		t.Errorf("at quota: wait=%s err=%v", wait, err)
	}
	if _, err := q.Check(context.Background(), 1, 0); err != nil {
		t.Error("quota 0 must be unlimited")
	}
	u.err = errors.New("db down")
	if _, err := q.Check(context.Background(), 1, 1000); err == nil || errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("db error: %v", err)
	}
}

func TestGateQueueFullAndTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := NewGate(1, 1, 30*time.Second)
		ctx := context.Background()
		release, _, err := g.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := make(chan error, 1)
		go func() {
			_, waited, err := g.Acquire(ctx)
			if waited != 30*time.Second {
				t.Errorf("waited %s", waited)
			}
			got <- err
		}()
		synctest.Wait()
		if in, q := g.Stats(); in != 1 || q != 1 {
			t.Fatalf("stats %d/%d", in, q)
		}
		if _, _, err := g.Acquire(ctx); !errors.Is(err, ErrQueueFull) {
			t.Fatalf("third: %v, want ErrQueueFull", err)
		}
		time.Sleep(30 * time.Second)
		if err := <-got; !errors.Is(err, ErrQueueTimeout) {
			t.Fatalf("waiter: %v, want ErrQueueTimeout", err)
		}
		release()
		release()
		if in, q := g.Stats(); in != 0 || q != 0 {
			t.Fatalf("stats after release %d/%d", in, q)
		}
	})
}

func TestGateHandsSlotToWaiterInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := NewGate(1, 3, time.Minute)
		release, _, _ := g.Acquire(context.Background())
		order := make(chan int, 3)
		var wg sync.WaitGroup
		defer wg.Wait()
		for i := range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, _, err := g.Acquire(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				order <- i
				time.Sleep(time.Second)
				r()
			}()
			synctest.Wait() // enqueue in a known order
		}
		release()
		for want := range 3 {
			if got := <-order; got != want {
				t.Fatalf("slot %d went to waiter %d", want, got)
			}
		}
	})
}

func TestGateCancelledWaiterLeaves(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := NewGate(1, 4, time.Minute)
		release, _, _ := g.Acquire(context.Background())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, _, err := g.Acquire(ctx)
			done <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
		release()
		if in, q := g.Stats(); in != 0 || q != 0 {
			t.Fatalf("stats %d/%d", in, q)
		}
	})
}

func TestGateNoQueue(t *testing.T) {
	g := NewGate(1, 0, time.Minute)
	_, _, _ = g.Acquire(context.Background())
	if _, _, err := g.Acquire(context.Background()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("got %v", err)
	}
}

func TestRateLimiterRefund(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	l := NewRateLimiter(clk.now)
	for range 3 {
		l.Allow(1, 6, 3)
	}
	if ok, _ := l.Allow(1, 6, 3); ok {
		t.Fatal("bucket not empty")
	}
	l.Refund(1, 6, 3)
	if ok, _ := l.Allow(1, 6, 3); !ok {
		t.Fatal("refunded token not available")
	}
	// A refund never lifts the bucket above burst.
	l.Allow(2, 6, 3)
	l.Refund(2, 6, 3)
	l.Refund(2, 6, 3)
	for i := range 3 {
		if ok, _ := l.Allow(2, 6, 3); !ok {
			t.Fatalf("request %d refused", i)
		}
	}
	if ok, _ := l.Allow(2, 6, 3); ok {
		t.Fatal("refund exceeded burst")
	}
	l.Refund(3, 0, 3) // rpm 0: no bucket, no panic
	l.Refund(4, 6, 3) // unknown key: no-op
}

func TestRateLimiterPeek(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	l := NewRateLimiter(clk.now)
	for range 5 {
		if ok, _ := l.Peek(1, 30, 1); !ok {
			t.Fatal("peek refused a full bucket")
		}
	}
	l.Allow(1, 30, 1)
	if ok, wait := l.Peek(1, 30, 1); ok || wait != 2*time.Second {
		t.Fatalf("peek on empty bucket: ok=%v wait=%s", ok, wait)
	}
}
